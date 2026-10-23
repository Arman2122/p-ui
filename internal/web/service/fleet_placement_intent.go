package service

import (
	"bytes"
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func reconcileFleetPlacementClientTx(tx *gorm.DB, clientID string) (bool, error) {
	source, err := loadFleetPlacementSource(tx, clientID)
	if err != nil {
		return false, err
	}
	projection, err := projectFleetPlacementTx(tx, source)
	if err != nil {
		return false, err
	}
	var existing []model.FleetPlacementIntent
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("client_id = ?", clientID).Order("id").Limit(8001).Find(&existing).Error; err != nil {
		return false, err
	}
	if len(existing) > 8000 {
		return false, controlplane.ErrInvalid
	}
	byScope := make(map[string]model.FleetPlacementIntent, len(existing))
	for _, row := range existing {
		byScope[row.NodeID+"/"+row.InstallationID+"/"+row.RuntimeID] = row
	}
	seen := map[string]bool{}
	unresolved := map[string]bool{}
	for _, target := range projection.Targets {
		if target.RuntimeID == "" || target.NodePolicyRevision == 0 {
			unresolved[target.NodeID+"/"+target.InstallationID+"/"+target.CoreID] = true
			continue
		}
		key := target.NodeID + "/" + target.InstallationID + "/" + target.RuntimeID
		seen[key] = true
		if err := putFleetPlacementIntent(tx, source, target, byScope[key]); err != nil {
			return false, err
		}
	}
	for _, row := range existing {
		key := row.NodeID + "/" + row.InstallationID + "/" + row.RuntimeID
		if seen[key] || unresolved[row.NodeID+"/"+row.InstallationID+"/"+row.CoreID] || !row.Assigned {
			continue
		}
		if row.Revision == math.MaxInt64 {
			return false, controlplane.ErrRevision
		}
		row.Revision++
		row.Assigned, row.Pending, row.State, row.SourceRevision = false, true, "retiring", source.client.PlacementRevision
		row.AvailableUnixMilli = time.Now().UnixMilli()
		if row.MaterializedPlacementID != nil {
			var placement model.FleetPlacement
			if err := tx.First(&placement, "id = ?", *row.MaterializedPlacementID).Error; err != nil {
				return false, err
			}
			row.RequiresStop = true
			operation := uuid.NewSHA1(uuid.MustParse(row.ID), []byte("stop/"+strconv.FormatInt(row.Revision, 10))).String()
			if err := queueFleetPlacementStopTx(tx, source.client, source.source, placement, operation); err != nil {
				return false, err
			}
		}
		if err := tx.Save(&row).Error; err != nil {
			return false, err
		}
	}
	preferences, err := json.Marshal(source.preferences)
	if err != nil {
		return false, err
	}
	if len(preferences) > 1<<20 {
		return false, controlplane.ErrInvalid
	}
	if err := tx.Model(&source.client).Update("placement_preferences", string(preferences)).Error; err != nil {
		return false, err
	}
	return len(unresolved) == 0, nil
}

func putFleetPlacementIntent(tx *gorm.DB, source fleetPlacementSource, target FleetPlacementTarget, current model.FleetPlacementIntent) error {
	ids, err := json.Marshal(target.InboundIDs)
	if err != nil {
		return err
	}
	pins, err := json.Marshal(target.Rules)
	if err != nil {
		return err
	}
	digest, err := fleetDigest(struct {
		SourceRevision, CredentialGeneration, NodePolicyRevision, DefaultRevision uint64
		InboundIDs                                                                []int
		Rules                                                                     []FleetPlacementRulePin
	}{uint64(source.client.PlacementRevision), uint64(source.client.CredentialGeneration), target.NodePolicyRevision, target.DefaultRevision, target.InboundIDs, target.Rules})
	if err != nil {
		return err
	}
	if current.ID != "" && current.Assigned && bytes.Equal(current.Digest, digest) {
		if current.State == "awaiting-source" && source.source.Enable {
			return tx.Model(&current).Updates(map[string]any{"state": "pending", "pending": true, "available_unix_milli": time.Now().UnixMilli()}).Error
		}
		return nil
	}
	if current.Revision == math.MaxInt64 {
		return controlplane.ErrRevision
	}
	var physical model.FleetPlacement
	query := tx.Where("client_id = ? AND node_id = ? AND installation_id = ? AND runtime_id = ?", source.client.ID, target.NodeID, target.InstallationID, target.RuntimeID).Find(&physical)
	if query.Error != nil {
		return query.Error
	}
	changedShape := false
	if query.RowsAffected != 0 {
		if physical.CoreID != target.CoreID {
			return controlplane.ErrScope
		}
		actual, err := placementInboundIDs(tx, physical.ID)
		if err != nil {
			return err
		}
		changedShape = !slices.Equal(actual, target.InboundIDs) || physical.CredentialGeneration != source.client.CredentialGeneration
		current.MaterializedPlacementID = &physical.ID
	}
	previouslyApplied := current.Assigned && current.State == "applied" && current.AppliedRevision > 0 && !changedShape && !current.RequiresStop
	if current.ID == "" {
		current.ID = uuid.NewSHA1(uuid.MustParse(source.client.ID), []byte("placement/"+target.NodeID+"/"+target.InstallationID+"/"+target.RuntimeID)).String()
		current.QueuedUnixMilli = time.Now().UnixMilli()
	}
	current.ClientID, current.NodeID, current.InstallationID, current.RuntimeID, current.CoreID = source.client.ID, target.NodeID, target.InstallationID, target.RuntimeID, target.CoreID
	current.Revision++
	current.SourceRevision, current.CredentialGeneration = source.client.PlacementRevision, source.client.CredentialGeneration
	current.NodePolicyRevision, current.DefaultRevision = int64(target.NodePolicyRevision), int64(target.DefaultRevision)
	current.RulePins, current.InboundIDs, current.Digest = string(pins), string(ids), digest
	current.Assigned, current.Pending, current.State = true, !previouslyApplied, "pending"
	if previouslyApplied {
		current.State = "applied"
	}
	current.RequiresStop = current.RequiresStop || changedShape
	current.AvailableUnixMilli = time.Now().UnixMilli()
	if current.RequiresStop && query.RowsAffected != 0 {
		operation := uuid.NewSHA1(uuid.MustParse(current.ID), []byte("stop/"+strconv.FormatInt(current.Revision, 10))).String()
		if err := queueFleetPlacementStopTx(tx, source.client, source.source, physical, operation); err != nil {
			return err
		}
	}
	return tx.Save(&current).Error
}
