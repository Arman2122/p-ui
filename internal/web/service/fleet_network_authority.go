package service

import (
	"encoding/json"
	"slices"
	"strconv"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func lockFleetNetworkBudgets(tx *gorm.DB, snapshots []fleetClientSnapshot) error {
	var scopes, budgets, rates []string
	for _, snapshot := range snapshots {
		if snapshot.Owner != nil {
			scopes = append(scopes, "reseller/"+strconv.Itoa(snapshot.Owner.UserID))
		} else if snapshot.Group != nil {
			scopes = append(scopes, "group/"+strconv.Itoa(snapshot.Group.Id))
		}
		budgets = append(budgets, snapshot.Term.BudgetID)
		rates = append(rates, snapshot.Fleet.ID)
	}
	slices.Sort(scopes)
	for _, scope := range slices.Compact(scopes) {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || '.fleet-parent/' || ?, 0))", scope).Error; err != nil {
			return err
		}
	}
	for _, snapshot := range snapshots {
		parents, _, err := fleetParentBudgets(tx, snapshot)
		if err != nil {
			return err
		}
		budgets = append(budgets, parents...)
	}
	slices.Sort(budgets)
	budgets = slices.Compact(budgets)
	for offset := 0; offset < len(budgets); offset += 1024 {
		chunk := budgets[offset:min(offset+1024, len(budgets))]
		var rows []model.FleetBudget
		if err := tx.Select("id").Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ?", chunk).Order("id").Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) != len(chunk) {
			return controlplane.ErrScope
		}
	}
	slices.Sort(rates)
	rates = slices.Compact(rates)
	for offset := 0; offset < len(rates); offset += 1024 {
		chunk := rates[offset:min(offset+1024, len(rates))]
		var rows []model.FleetRateBudget
		if err := tx.Select("id").Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ?", chunk).Order("id").Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) != len(chunk) {
			return controlplane.ErrScope
		}
	}
	return nil
}

func requireFleetNetworkRetirements(tx *gorm.DB, node model.FleetNode, included map[string]bool) error {
	var grants []model.FleetGrant
	return tx.Where("node_id = ? AND installation_id = ?", node.ID, node.InstallationID).FindInBatches(&grants, 1000, func(_ *gorm.DB, _ int) error {
		var ids []string
		for _, grant := range grants {
			if !included[grant.ID] {
				ids = append(ids, grant.ID)
			}
		}
		if len(ids) == 0 {
			return nil
		}
		var rows []struct {
			model.FleetRateShare
			StopRevision       int64
			DeploymentRevision int64
		}
		if err := tx.Table("fleet_rate_shares s").Select("s.*, stop.rate_revision AS stop_revision, stop.deployment_revision").
			Joins("JOIN fleet_grant_stops stop ON stop.grant_id = s.id").Where("s.id IN ?", ids).Scan(&rows).Error; err != nil {
			return err
		}
		if len(rows) != len(ids) {
			return controlplane.ErrDrain
		}
		for _, row := range rows {
			var desired, outstanding controlplane.Rates
			if json.Unmarshal([]byte(row.Desired), &desired) != nil || json.Unmarshal([]byte(row.Outstanding), &outstanding) != nil {
				return ErrFleetNetwork
			}
			if row.NodeID != node.ID || row.InstallationID != node.InstallationID || row.AppliedRevision != row.Revision || row.StopRevision > row.AppliedRevision ||
				row.DeploymentRevision <= 0 || row.DeploymentRevision > node.AppliedRevision || desired != (controlplane.Rates{}) || outstanding != (controlplane.Rates{}) {
				return controlplane.ErrDrain
			}
		}
		return nil
	}).Error
}
