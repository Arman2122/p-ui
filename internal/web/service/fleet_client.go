package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"math"
	"regexp"
	"strconv"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

type FleetClientService struct{}

type FleetMaterializeRequest struct {
	PanelClientID                             int
	NodeID, InstallationID, RuntimeID, CoreID string
	InboundIDs                                []int
}

type FleetMaterialization struct {
	Client    model.FleetClient
	Placement model.FleetPlacement
	Inbounds  []model.FleetPlacementInbound
}

type FleetApproveRequest struct {
	PlacementID                                       string
	ExpectedRevision                                  uint64
	DefinitionDigest, SourcePolicyDigest, GraphDigest []byte
}

var fleetRuntimeName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)

func fleetID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func lockFleetNode(tx *gorm.DB, nodeID, installationID string) error {
	if _, err := controlplane.NewStore(tx); err != nil {
		return err
	}
	var fence model.FleetFence
	if err := tx.Clauses(clause.Locking{Strength: "SHARE"}).First(&fence, "id = 1").Error; err != nil {
		return err
	}
	if fence.RecoveryRequired {
		return controlplane.ErrRecovery
	}
	var node model.FleetNode
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&node, "id = ?", nodeID).Error; err != nil {
		return err
	}
	if !node.Enabled || node.InstallationID != installationID {
		return ErrFleetSource
	}
	return nil
}

func (FleetClientService) Materialize(ctx context.Context, actor *model.User, request FleetMaterializeRequest) (FleetMaterialization, error) {
	var result FleetMaterialization
	err := database.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireFleetAdmin(tx, actor); err != nil {
			return err
		}
		var err error
		result, err = materializeFleetClientTx(ctx, tx, request)
		return err
	})
	return result, err
}

func materializeFleetClientTx(ctx context.Context, tx *gorm.DB, request FleetMaterializeRequest) (FleetMaterialization, error) {
	var result FleetMaterialization
	if !fleetID(request.NodeID) || !fleetID(request.InstallationID) || !fleetRuntimeName.MatchString(request.RuntimeID) || !fleetRuntimeName.MatchString(request.CoreID) {
		return result, ErrFleetSource
	}
	err := func() error {
		if err := lockFleetNode(tx, request.NodeID, request.InstallationID); err != nil {
			return err
		}
		snapshot, err := fleetSnapshot(tx, request.PanelClientID, request.InboundIDs, request.CoreID)
		if err != nil {
			return err
		}
		store, err := controlplane.NewStore(tx)
		if err != nil {
			return err
		}
		client := snapshot.Fleet
		if client.ID == "" {
			client, err = store.BindClient(ctx, request.PanelClientID, snapshot.CredentialDigest)
		} else if !bytes.Equal(client.CredentialDigest, snapshot.CredentialDigest) {
			client, err = store.ReconcileCredentials(ctx, client.ID, uint64(client.CredentialGeneration), snapshot.CredentialDigest)
		}
		if err != nil {
			return err
		}
		var placement model.FleetPlacement
		found := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("client_id = ? AND node_id = ? AND installation_id = ? AND runtime_id = ?", client.ID, request.NodeID, request.InstallationID, request.RuntimeID).Find(&placement)
		if found.Error != nil {
			return found.Error
		}
		unchanged := found.RowsAffected != 0 && placement.CoreID == request.CoreID && placement.CredentialGeneration == client.CredentialGeneration &&
			bytes.Equal(placement.SourcePolicyDigest, snapshot.PolicyDigest) && bytes.Equal(placement.DefinitionDigest, snapshot.DefinitionDigest)
		if !unchanged {
			if placement.Revision == math.MaxInt64 {
				return controlplane.ErrRevision
			}
			if placement.ID == "" {
				placement.ID = uuid.NewString()
			}
			placement.ClientID, placement.NodeID, placement.InstallationID, placement.RuntimeID, placement.CoreID = client.ID, request.NodeID, request.InstallationID, request.RuntimeID, request.CoreID
			placement.CredentialGeneration, placement.SourcePolicyDigest, placement.DefinitionDigest = client.CredentialGeneration, snapshot.PolicyDigest, snapshot.DefinitionDigest
			placement.Revision++
			placement.Enabled, placement.GraphDigest = false, nil
			if err := tx.Save(&placement).Error; err != nil {
				return err
			}
			if err := tx.Where("placement_id = ?", placement.ID).Delete(&model.FleetPlacementInbound{}).Error; err != nil {
				return err
			}
			for _, inbound := range snapshot.Inbounds {
				id := inbound.Id
				instanceID := uuid.NewSHA1(uuid.MustParse(request.InstallationID), []byte(request.RuntimeID+"/inbound/"+strconv.Itoa(id))).String()
				result.Inbounds = append(result.Inbounds, model.FleetPlacementInbound{PlacementID: placement.ID, InstanceID: instanceID, PanelInbound: &id})
			}
			if err := tx.Create(&result.Inbounds).Error; err != nil {
				return err
			}
		} else if err := tx.Where("placement_id = ?", placement.ID).Order("instance_id").Find(&result.Inbounds).Error; err != nil {
			return err
		}
		result.Client, result.Placement = client, placement
		return nil
	}()
	return result, err
}

// Approve records the master's validated graph; it is not a substitute for compiling or checking that graph.
func (FleetClientService) Approve(ctx context.Context, actor *model.User, request FleetApproveRequest) (model.FleetPlacement, error) {
	var result model.FleetPlacement
	if !fleetID(request.PlacementID) || request.ExpectedRevision == 0 || request.ExpectedRevision > math.MaxInt64 ||
		len(request.DefinitionDigest) != sha256.Size || len(request.SourcePolicyDigest) != sha256.Size || len(request.GraphDigest) != sha256.Size {
		return result, ErrFleetSource
	}
	err := submitTrafficWrite(func() error {
		return database.GetDB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := requireFleetAdmin(tx, actor); err != nil {
				return err
			}
			snapshot, placement, _, err := fleetPlacementSnapshot(tx, request.PlacementID)
			if err != nil {
				return err
			}
			if uint64(placement.Revision) != request.ExpectedRevision || !bytes.Equal(request.DefinitionDigest, snapshot.DefinitionDigest) ||
				!bytes.Equal(request.SourcePolicyDigest, snapshot.PolicyDigest) {
				return ErrFleetSource
			}
			if placement.Enabled && !bytes.Equal(placement.GraphDigest, request.GraphDigest) {
				return controlplane.ErrConflict
			}
			if !snapshot.Fleet.Owned {
				var live int64
				if err := tx.Table("client_inbounds ci").Joins("JOIN inbounds i ON i.id = ci.inbound_id").Where("ci.client_id = ? AND (i.enable OR i.node_id IS NOT NULL)", snapshot.Client.Id).Count(&live).Error; err != nil {
					return err
				}
				if live != 0 || snapshot.Fleet.Revision == math.MaxInt64 {
					return ErrFleetSource
				}
				if err := tx.Model(&model.FleetClient{}).Where("id = ?", snapshot.Fleet.ID).Updates(map[string]any{"owned": true, "revision": snapshot.Fleet.Revision + 1}).Error; err != nil {
					return err
				}
			}
			placement.Enabled, placement.GraphDigest = true, bytes.Clone(request.GraphDigest)
			if err := tx.Model(&placement).Updates(map[string]any{"enabled": true, "graph_digest": placement.GraphDigest}).Error; err != nil {
				return err
			}
			result = placement
			return nil
		})
	})
	return result, err
}

func fleetPlacementSnapshot(tx *gorm.DB, id string) (fleetClientSnapshot, model.FleetPlacement, []model.FleetPlacementInbound, error) {
	var snapshot fleetClientSnapshot
	var placement model.FleetPlacement
	var refs []model.FleetPlacementInbound
	if !fleetID(id) {
		return snapshot, placement, refs, ErrFleetSource
	}
	if err := tx.First(&placement, "id = ?", id).Error; err != nil {
		return snapshot, placement, refs, err
	}
	if err := lockFleetNode(tx, placement.NodeID, placement.InstallationID); err != nil {
		return snapshot, placement, refs, err
	}
	var client model.FleetClient
	if err := tx.First(&client, "id = ?", placement.ClientID).Error; err != nil {
		return snapshot, placement, refs, err
	}
	if client.PanelClientID == nil {
		return snapshot, placement, refs, ErrFleetSource
	}
	if err := tx.Where("placement_id = ?", id).Order("instance_id").Find(&refs).Error; err != nil {
		return snapshot, placement, refs, err
	}
	ids := make([]int, len(refs))
	for i, ref := range refs {
		if ref.PanelInbound == nil {
			return snapshot, placement, refs, ErrFleetSource
		}
		ids[i] = *ref.PanelInbound
	}
	var err error
	snapshot, err = fleetSnapshot(tx, *client.PanelClientID, ids, placement.CoreID)
	if err != nil {
		return snapshot, placement, refs, err
	}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&placement, "id = ?", id).Error; err != nil {
		return snapshot, placement, refs, err
	}
	if placement.CredentialGeneration != snapshot.Fleet.CredentialGeneration || !bytes.Equal(snapshot.CredentialDigest, snapshot.Fleet.CredentialDigest) ||
		!bytes.Equal(placement.SourcePolicyDigest, snapshot.PolicyDigest) || !bytes.Equal(placement.DefinitionDigest, snapshot.DefinitionDigest) {
		return snapshot, placement, refs, ErrFleetSource
	}
	return snapshot, placement, refs, nil
}

func fleetTermSpec(snapshot fleetClientSnapshot, kind string) (controlplane.TermSpec, error) {
	spec := controlplane.TermSpec{QuotaBytes: snapshot.Client.TotalGB, Unlimited: snapshot.Client.TotalGB == 0}
	if !snapshot.Client.Enable || snapshot.Traffic.Up < 0 || snapshot.Traffic.Down < 0 || snapshot.Traffic.Up > math.MaxInt64-snapshot.Traffic.Down {
		return spec, ErrFleetSource
	}
	if snapshot.Assignment != nil && !snapshot.Assignment.OverrideQuota && snapshot.Assignment.AppliedOnDepletion != "" && snapshot.Assignment.AppliedOnDepletion != "disable" {
		return spec, ErrFleetPhase
	}
	if snapshot.Owner != nil && snapshot.Owner.TrafficLimit > 0 && snapshot.Owner.OnDepletion != "" && snapshot.Owner.OnDepletion != "disable" {
		return spec, ErrFleetPhase
	}
	if kind == controlplane.Activation {
		spec.OpeningUsedBytes = snapshot.Traffic.Up + snapshot.Traffic.Down
		spec.OpeningUploaded, spec.OpeningDownloaded = snapshot.Traffic.Up, snapshot.Traffic.Down
		if snapshot.Client.ExpiryTime < 0 {
			spec.DurationMillis = -snapshot.Client.ExpiryTime
		} else {
			spec.ExpiresUnixMilli = snapshot.Client.ExpiryTime
		}
	} else if kind == controlplane.Renewal {
		if snapshot.Client.Reset <= 0 || snapshot.Client.Reset > 36500 || snapshot.Owner != nil {
			return spec, ErrFleetSource
		}
		spec.DurationMillis = int64(snapshot.Client.Reset) * 86400000
	} else if kind != controlplane.TrafficReset {
		return spec, ErrFleetSource
	}
	if snapshot.Owner != nil && snapshot.Owner.ExpiresAt > 0 && (snapshot.Client.ExpiryTime <= 0 || snapshot.Client.ExpiryTime > snapshot.Owner.ExpiresAt) {
		return spec, ErrResellerBudget
	}
	spec.Rates = fleetRates(snapshot.Tiers, spec.OpeningUsedBytes)
	return spec, nil
}

func fleetEntitlementMatches(snapshot fleetClientSnapshot) error {
	if snapshot.Term == nil || snapshot.Client.ExpiryTime != snapshot.Term.ExpiresUnixMilli {
		return ErrFleetSource
	}
	if !snapshot.Client.Enable || snapshot.Term.ExpiresUnixMilli != 0 && snapshot.Term.ExpiresUnixMilli <= time.Now().UnixMilli() {
		return ErrFleetSource
	}
	if snapshot.Owner != nil && snapshot.Owner.ExpiresAt > 0 && (snapshot.Term.ExpiresUnixMilli == 0 || snapshot.Term.ExpiresUnixMilli > snapshot.Owner.ExpiresAt) {
		return ErrResellerBudget
	}
	return nil
}
