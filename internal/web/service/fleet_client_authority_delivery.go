package service

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"

	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/authority"
)

func ValidateFleetAuthorityDeliveryTx(ctx context.Context, tx *gorm.DB, updates []model.FleetAuthorityUpdate) (map[string]bool, error) {
	allowed := make(map[string]bool, len(updates))
	if tx == nil || len(updates) > 128 {
		return nil, controlplane.ErrInvalid
	}
	if len(updates) == 0 {
		return allowed, nil
	}
	ids := make([]string, 0, len(updates))
	byID := make(map[string]model.FleetAuthorityUpdate, len(updates))
	messages := make(map[string]*nodev1.AuthorityUpdate, len(updates))
	scope := updates[0]
	for _, update := range updates {
		if update.NodeID != scope.NodeID || update.InstallationID != scope.InstallationID || update.RuntimeID != scope.RuntimeID || byID[update.GrantID].GrantID != "" {
			return nil, controlplane.ErrScope
		}
		digest := sha256.Sum256(update.Payload)
		message := new(nodev1.AuthorityUpdate)
		if !bytes.Equal(digest[:], update.PayloadSHA256) || proto.Unmarshal(update.Payload, message) != nil {
			return nil, controlplane.ErrRecovery
		}
		policy, err := authority.AuthorityPolicy(uint64(update.BaseConfigurationRevision), message)
		if err != nil {
			return nil, controlplane.ErrRecovery
		}
		byID[update.GrantID] = update
		if !policy.Enabled {
			allowed[update.GrantID] = true
			continue
		}
		ids, byID[update.GrantID] = append(ids, update.GrantID), update
		messages[update.GrantID] = message
	}
	if len(ids) == 0 {
		return allowed, nil
	}
	var candidates []struct{ GrantID, PlacementID string }
	if err := tx.WithContext(ctx).Table("fleet_grants g").Select("g.id AS grant_id,p.id AS placement_id").
		Joins("JOIN fleet_placements p ON p.id = g.placement_id").Joins("JOIN fleet_clients c ON c.id = p.client_id").
		Where("g.id IN ? AND c.panel_client_id IS NOT NULL AND p.node_id = ? AND p.installation_id = ? AND p.runtime_id = ? AND g.client_id = p.client_id", ids, scope.NodeID, scope.InstallationID, scope.RuntimeID).
		Order("c.panel_client_id,g.id").Scan(&candidates).Error; err != nil {
		return nil, err
	}
	for _, candidate := range candidates {
		queued := byID[candidate.GrantID]
		snapshot, placement, _, err := fleetPlacementSnapshot(tx, candidate.PlacementID)
		if err != nil {
			if errors.Is(err, ErrFleetSource) || errors.Is(err, ErrResellerForbidden) || errors.Is(err, ErrResellerBudget) || errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return nil, err
		}
		if !snapshot.Fleet.Owned || !placement.Enabled || len(placement.GraphDigest) != sha256.Size || snapshot.Term == nil || snapshot.Term.ID != queued.TermID || snapshot.Term.State != "active" ||
			placement.CurrentGrantID == nil || *placement.CurrentGrantID != queued.GrantID || placement.CurrentGrantTermID == nil || *placement.CurrentGrantTermID != queued.TermID ||
			placement.ID != queued.PlacementID || placement.NodeID != queued.NodeID || placement.InstallationID != queued.InstallationID || placement.RuntimeID != queued.RuntimeID || placement.ClientID != queued.ClientID ||
			fleetEntitlementMatches(snapshot) != nil {
			continue
		}
		pendingStop, err := fleetPlacementStopPending(tx, placement.ID)
		if err != nil {
			return nil, err
		}
		if pendingStop {
			continue
		}
		if err := requireFleetPlacementSourceReadyTx(tx, placement); err != nil {
			if errors.Is(err, controlplane.ErrDrain) {
				continue
			}
			return nil, err
		}
		published, err := controlplane.PublishedAuthorityScopeTx(tx, placement.NodeID, uint64(queued.BaseConfigurationRevision), placement.RuntimeID, snapshot.Fleet.ID)
		if errors.Is(err, controlplane.ErrScope) || errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		message := messages[queued.GrantID]
		if published.CoreID != placement.CoreID || published.Descriptor.PlacementID != placement.ID || published.Descriptor.PlacementRevision != uint64(placement.Revision) ||
			published.Descriptor.CredentialGeneration != uint64(snapshot.Fleet.CredentialGeneration) || message.PlacementId != placement.ID || message.PlacementRevision != uint64(placement.Revision) ||
			message.CredentialGeneration != uint64(snapshot.Fleet.CredentialGeneration) || !bytes.Equal(message.DescriptorSha256, published.Descriptor.SHA256) || !bytes.Equal(queued.DescriptorSHA256, published.Descriptor.SHA256) {
			continue
		}
		var budget model.FleetBudget
		if err := tx.First(&budget, "id = ?", snapshot.Term.BudgetID).Error; err != nil {
			return nil, err
		}
		if budget.Unlimited != (snapshot.Client.TotalGB == 0) || !budget.Unlimited && budget.LimitBytes != snapshot.Client.TotalGB {
			continue
		}
		var pool model.FleetRateBudget
		if err := tx.First(&pool, "id = ?", snapshot.Fleet.ID).Error; err != nil {
			return nil, err
		}
		var desired controlplane.Rates
		if json.Unmarshal([]byte(pool.Desired), &desired) != nil {
			return nil, controlplane.ErrRecovery
		}
		if !fleetRates(snapshot.Tiers, budget.UsedBytes).Contains(desired) {
			continue
		}
		allowed[queued.GrantID] = true
	}
	return allowed, nil
}
