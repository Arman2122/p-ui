package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func lockFleetPlacementIntent(tx *gorm.DB, expected model.FleetPlacementIntent) (model.FleetPlacementIntent, error) {
	var current model.FleetPlacementIntent
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&current, "id = ?", expected.ID).Error; err != nil {
		return current, err
	}
	if !current.Pending || current.Revision != expected.Revision || current.ClientID != expected.ClientID || current.NodeID != expected.NodeID || current.InstallationID != expected.InstallationID || current.RuntimeID != expected.RuntimeID {
		return current, controlplane.ErrRevision
	}
	return current, nil
}

func ProcessFleetPlacementIntent(ctx context.Context, db *gorm.DB, expected model.FleetPlacementIntent) (model.FleetPlacementIntent, error) {
	var current model.FleetPlacementIntent
	if db == nil || !fleetID(expected.ID) || !fleetID(expected.ClientID) || !fleetID(expected.NodeID) || !fleetID(expected.InstallationID) || expected.Revision <= 0 {
		return current, controlplane.ErrInvalid
	}
	if _, nested := db.Statement.ConnPool.(gorm.TxCommitter); nested {
		return current, controlplane.ErrInvalid
	}
	remove := false
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockFleetNode(tx, expected.NodeID, expected.InstallationID); err != nil {
			return err
		}
		source, err := loadFleetPlacementSource(tx, expected.ClientID)
		if err != nil {
			return err
		}
		current, err = lockFleetPlacementIntent(tx, expected)
		if err != nil {
			return err
		}
		if current.SourceRevision != source.client.PlacementRevision || current.CredentialGeneration != source.client.CredentialGeneration {
			return controlplane.ErrRevision
		}
		var node model.FleetNode
		if err := tx.First(&node, "id = ?", current.NodeID).Error; err != nil {
			return err
		}
		if current.RequiresStop {
			var placement model.FleetPlacement
			if current.MaterializedPlacementID == nil {
				return controlplane.ErrRecovery
			}
			if err := tx.First(&placement, "id = ?", *current.MaterializedPlacementID).Error; err != nil {
				return err
			}
			work, err := fleetPlacementSourceWork(tx, current, placement)
			if err != nil {
				return err
			}
			stopped, revision, err := fleetSourceWorkStopped(tx, work)
			if err != nil {
				return err
			}
			if !stopped || work.RequiresStop {
				return saveFleetPlacementProgress(tx, &current)
			}
			current.RequiresStop = false
			current.AppliedStopRevision = max(current.AppliedStopRevision, revision)
		}
		if !current.Assigned {
			if current.MaterializedPlacementID == nil {
				current.State, current.Pending = "retired", false
			} else {
				applied, err := fleetPlacementOmissionApplied(tx, node, current)
				if err != nil {
					return err
				}
				if applied {
					current.State, current.Pending, current.AppliedRevision = "retired", false, node.AppliedRevision
				} else {
					remove = node.AppliedRevision == node.DesiredRevision
				}
			}
			return saveFleetPlacementProgress(tx, &current)
		}
		if source.client.PendingTermID != nil {
			return controlplane.ErrDrain
		}
		if !source.source.Enable {
			current.State, current.Pending = "awaiting-source", false
			return saveFleetPlacementProgress(tx, &current)
		}
		if err := applyFleetPlacementIntent(tx, source, node, &current); err != nil {
			return err
		}
		return saveFleetPlacementProgress(tx, &current)
	})
	if err == nil && remove {
		current, err = publishFleetPlacementRemoval(ctx, db, current)
	}
	if err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		if deferErr := deferFleetPlacementIntent(cleanup, db, expected, time.Second); deferErr != nil {
			return current, deferErr
		}
	}
	return current, err
}

func saveFleetPlacementProgress(tx *gorm.DB, current *model.FleetPlacementIntent) error {
	if current.Pending {
		current.AvailableUnixMilli = max(current.AvailableUnixMilli+1, time.Now().Add(250*time.Millisecond).UnixMilli())
	}
	return tx.Model(current).Updates(map[string]any{
		"requires_stop": current.RequiresStop, "pending": current.Pending, "state": current.State,
		"materialized_placement_id": current.MaterializedPlacementID, "applied_stop_revision": current.AppliedStopRevision, "applied_revision": current.AppliedRevision, "available_unix_milli": current.AvailableUnixMilli,
	}).Error
}

func deferFleetPlacementIntent(ctx context.Context, db *gorm.DB, expected model.FleetPlacementIntent, delay time.Duration) error {
	return db.WithContext(ctx).Model(&model.FleetPlacementIntent{}).Where("id = ? AND revision = ? AND pending", expected.ID, expected.Revision).
		Update("available_unix_milli", gorm.Expr("GREATEST(available_unix_milli + 1, ?)", time.Now().Add(delay).UnixMilli())).Error
}

func applyFleetPlacementIntent(tx *gorm.DB, source fleetPlacementSource, node model.FleetNode, intent *model.FleetPlacementIntent) error {
	projection, err := projectFleetPlacementTx(tx, source)
	if err != nil {
		return err
	}
	var selected *FleetPlacementTarget
	for _, target := range projection.Targets {
		if target.NodeID == intent.NodeID && target.InstallationID == intent.InstallationID && target.RuntimeID == intent.RuntimeID && target.CoreID == intent.CoreID {
			selected = &target
		}
	}
	if selected == nil || int64(selected.NodePolicyRevision) != intent.NodePolicyRevision || int64(selected.DefaultRevision) != intent.DefaultRevision {
		return controlplane.ErrRevision
	}
	ids, _ := json.Marshal(selected.InboundIDs)
	pins, _ := json.Marshal(selected.Rules)
	var expectedIDs []int
	var expectedPins []FleetPlacementRulePin
	if json.Unmarshal([]byte(intent.InboundIDs), &expectedIDs) != nil || json.Unmarshal([]byte(intent.RulePins), &expectedPins) != nil {
		return controlplane.ErrRecovery
	}
	encodedIDs, _ := json.Marshal(expectedIDs)
	encodedPins, _ := json.Marshal(expectedPins)
	if !bytes.Equal(ids, encodedIDs) || !bytes.Equal(pins, encodedPins) {
		return controlplane.ErrRevision
	}
	if !selected.ApplicationEligible {
		return ErrFleetNetworkDependency
	}
	if node.AppliedRevision != node.DesiredRevision {
		return nil
	}
	snapshot, err := fleetSnapshot(tx, source.source.Id, selected.InboundIDs, selected.CoreID)
	if err != nil {
		return err
	}
	if !bytes.Equal(snapshot.CredentialDigest, source.client.CredentialDigest) {
		return ErrFleetSource
	}
	materialized, err := materializeFleetClientTx(tx.Statement.Context, tx, FleetMaterializeRequest{PanelClientID: source.source.Id, NodeID: intent.NodeID, InstallationID: intent.InstallationID, RuntimeID: intent.RuntimeID, CoreID: intent.CoreID, InboundIDs: selected.InboundIDs})
	if err != nil {
		return err
	}
	intent.MaterializedPlacementID = &materialized.Placement.ID
	if err := tx.Model(intent).Update("materialized_placement_id", materialized.Placement.ID).Error; err != nil {
		return err
	}
	published, err := fleetPlacementPublished(tx, node, materialized.Placement.ID, intent.AppliedStopRevision)
	if err != nil && !errors.Is(err, ErrFleetSource) {
		return err
	}
	if published {
		intent.State, intent.Pending, intent.AppliedRevision = "applied", false, node.AppliedRevision
		return nil
	}
	var work model.FleetSourceWork
	query := tx.Where("placement_id = ?", materialized.Placement.ID).Find(&work)
	if query.Error != nil {
		return query.Error
	}
	if query.RowsAffected != 0 && work.Enabled && !work.RequiresStop {
		if work.Pending {
			return nil
		}
	}
	if work.RequiresStop || work.Revision == math.MaxInt64 {
		return controlplane.ErrDrain
	}
	operation := uuid.NewSHA1(uuid.MustParse(intent.ID), []byte("apply/"+strconv.FormatInt(intent.Revision, 10)+"/"+strconv.FormatInt(node.AppliedRevision, 10))).String()
	work = model.FleetSourceWork{
		PlacementID: materialized.Placement.ID, ClientID: source.client.ID, NodeID: node.ID, InstallationID: node.InstallationID, RuntimeID: intent.RuntimeID,
		OperationID: operation, Revision: work.Revision + 1, SourceRevision: materialized.Client.Revision, Enabled: true, Pending: true,
		AppliedStopRevision: max(work.AppliedStopRevision, intent.AppliedStopRevision, node.AppliedRevision), QueuedUnixMilli: time.Now().UnixMilli(), AvailableUnixMilli: time.Now().UnixMilli(),
	}
	return tx.Clauses(clause.OnConflict{UpdateAll: true}).Create(&work).Error
}

func publishFleetPlacementRemoval(ctx context.Context, db *gorm.DB, expected model.FleetPlacementIntent) (model.FleetPlacementIntent, error) {
	var current model.FleetPlacementIntent
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockFleetNode(tx, expected.NodeID, expected.InstallationID); err != nil {
			return err
		}
		state, err := loadFleetNetworkState(tx, expected.NodeID, expected.InstallationID)
		if err != nil {
			return err
		}
		if err := lockFleetSourceNodeClients(tx, state.Node); err != nil {
			return err
		}
		current, err = lockFleetPlacementIntent(tx, expected)
		if err != nil {
			return err
		}
		if current.Assigned || current.RequiresStop {
			return controlplane.ErrRevision
		}
		_, retired, err := fleetPlacementRetirementIdentity(tx, state.Node, current)
		if err != nil {
			return err
		}
		if !retired {
			return controlplane.ErrDrain
		}
		applied, err := fleetPlacementOmissionApplied(tx, state.Node, current)
		if err != nil {
			return err
		}
		if applied {
			current.State, current.Pending, current.AppliedRevision = "retired", false, state.Node.AppliedRevision
			return saveFleetPlacementProgress(tx, &current)
		}
		if state.Node.AppliedRevision != state.Node.DesiredRevision {
			return nil
		}
		if err := requireAppliedFleetNetworkTx(tx, state); err != nil {
			return err
		}
		preview, err := previewFleetNetworkTx(tx, FleetNetworkPreviewRequest{NodeID: state.Node.ID, InstallationID: state.Node.InstallationID, ExpectedRevision: uint64(state.Head.Revision), ExpectedDefaultsID: state.Head.DesiredDefaultsID}, 0)
		if err != nil {
			return err
		}
		if !preview.Ready {
			return ErrFleetNetworkDependency
		}
		operation := uuid.NewSHA1(uuid.MustParse(current.ID), []byte("remove/"+strconv.FormatInt(current.Revision, 10)+"/"+strconv.FormatInt(state.Node.AppliedRevision, 10))).String()
		if _, err := publishFleetNetworkTx(ctx, tx, FleetNetworkPublishRequest{OperationID: operation, CompilationID: preview.ID}, nil); err != nil {
			return err
		}
		current.State = "awaiting-apply"
		return saveFleetPlacementProgress(tx, &current)
	})
	return current, err
}
