package service

import (
	"context"
	"crypto/sha256"
	"errors"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/authority"
)

// Source edits must invalidate placements atomically before this fresh-snapshot gate can be optimized away.
func ValidateFleetCreditDeliveryTx(ctx context.Context, tx *gorm.DB, updates []model.FleetCreditUpdate) (map[string]bool, error) {
	allowed := make(map[string]bool, len(updates))
	if tx == nil || len(updates) > authority.MaxCreditUpdates {
		return nil, controlplane.ErrInvalid
	}
	if len(updates) == 0 {
		return allowed, nil
	}
	scope := updates[0]
	ids := make([]string, len(updates))
	byID := make(map[string]model.FleetCreditUpdate, len(updates))
	publications := map[int64]fleetNativeCreditPublication{}
	for i, update := range updates {
		if update.NodeID != scope.NodeID || update.InstallationID != scope.InstallationID || update.RuntimeID != scope.RuntimeID {
			return nil, controlplane.ErrScope
		}
		ids[i], byID[update.GrantID] = update.GrantID, update
	}
	var candidates []struct {
		GrantID, PlacementID string
	}
	if err := tx.WithContext(ctx).Table("fleet_grants g").Select("g.id AS grant_id, p.id AS placement_id").
		Joins("JOIN fleet_placements p ON p.id = g.placement_id").Joins("JOIN fleet_clients c ON c.id = p.client_id").
		Where("g.id IN ? AND c.panel_client_id IS NOT NULL AND p.node_id = ? AND p.installation_id = ? AND p.runtime_id = ? AND g.client_id = p.client_id", ids, scope.NodeID, scope.InstallationID, scope.RuntimeID).
		Order("c.panel_client_id, g.id").Scan(&candidates).Error; err != nil {
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
			placement.NodeID != queued.NodeID || placement.InstallationID != queued.InstallationID || placement.RuntimeID != queued.RuntimeID || placement.ClientID != queued.ClientID ||
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
		var grant model.FleetGrant
		if err := tx.First(&grant, "id = ?", queued.GrantID).Error; err != nil {
			return nil, err
		}
		var budget model.FleetBudget
		if err := tx.First(&budget, "id = ?", snapshot.Term.BudgetID).Error; err != nil {
			return nil, err
		}
		var share model.FleetRateShare
		if err := tx.First(&share, "id = ?", queued.GrantID).Error; err != nil {
			return nil, err
		}
		if grant.Revision != queued.Revision || grant.IssuedBytes != queued.CreditCeiling || grant.Unlimited || grant.NodeID != queued.NodeID || grant.InstallationID != queued.InstallationID ||
			grant.RuntimeID != queued.RuntimeID || grant.ClientID != queued.ClientID || grant.TermID != queued.TermID ||
			budget.Unlimited != (snapshot.Client.TotalGB == 0) || !budget.Unlimited && budget.LimitBytes != snapshot.Client.TotalGB || share.Revision != share.AppliedRevision {
			continue
		}
		current, err := currentFleetCreditPublication(tx, snapshot, placement, queued.BaseConfigurationRevision, grant, publications)
		if err != nil {
			return nil, err
		}
		if !current {
			continue
		}
		allowed[queued.GrantID] = true
	}
	return allowed, nil
}
