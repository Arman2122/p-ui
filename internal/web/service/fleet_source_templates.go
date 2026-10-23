package service

import (
	"slices"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func fleetEffectiveTemplateIDsTx(tx *gorm.DB, client model.ClientRecord, assignment *model.ClientPolicy, explicitIDs []int) ([]int, error) {
	return fleetEffectiveTemplateIDs(tx, client, assignment, explicitIDs, true)
}

func fleetEffectiveTemplateIDs(tx *gorm.DB, client model.ClientRecord, assignment *model.ClientPolicy, explicitIDs []int, lockRows bool) ([]int, error) {
	ids := slices.Clone(explicitIDs)
	if len(ids) > 4096 {
		return nil, ErrFleetSource
	}
	locked := tx
	if lockRows {
		locked = tx.Clauses(clause.Locking{Strength: "SHARE"}).Session(&gorm.Session{})
	}
	var managed []int
	if assignment != nil {
		if assignment.PolicyId == nil || assignment.ResolvedTiers != nil {
			return nil, ErrFleetSource
		}
		var plan model.Policy
		if err := locked.First(&plan, *assignment.PolicyId).Error; err != nil {
			return nil, err
		}
		if !sameOwner(plan.OwnerUserID, client.OwnerUserID) || plan.Archived || assignment.AppliedRev != plan.Rev {
			return nil, ErrFleetSource
		}
		if !assignment.OverrideInbounds {
			if err := tx.Model(&model.PolicyInbound{}).Where("policy_id = ?", plan.Id).Order("inbound_id").Limit(4097).Pluck("inbound_id", &managed).Error; err != nil {
				return nil, err
			}
			if len(managed) > 4096 {
				return nil, ErrFleetSource
			}
			ids = append(ids, managed...)
		}
	}
	if client.Group != "" {
		if client.OwnerUserID != nil {
			return nil, ErrResellerForbidden
		}
		var group model.ClientGroup
		if err := locked.Where("name = ?", client.Group).Find(&group).Error; err != nil {
			return nil, err
		}
		var shared []int
		if err := tx.Model(&model.GroupInbound{}).Where("name = ?", client.Group).Order("inbound_id").Limit(4097).Pluck("inbound_id", &shared).Error; err != nil {
			return nil, err
		}
		if len(shared) > 4096 || len(shared) > 0 && group.Id == 0 {
			return nil, ErrFleetSource
		}
		ids = append(ids, shared...)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if len(ids) > 4096 || slices.ContainsFunc(ids, func(id int) bool { return id <= 0 }) {
		return nil, ErrFleetSource
	}
	if len(managed) > 0 {
		ids = slices.DeleteFunc(ids, func(id int) bool { return !slices.Contains(managed, id) })
	}
	if client.OwnerUserID != nil && len(ids) > 0 {
		var granted []int
		if err := tx.Model(&model.ResellerInboundGrant{}).Where("user_id = ? AND inbound_id IN ?", *client.OwnerUserID, ids).Order("inbound_id").Pluck("inbound_id", &granted).Error; err != nil {
			return nil, err
		}
		ids = slices.DeleteFunc(ids, func(id int) bool { return !slices.Contains(granted, id) })
	}
	return ids, nil
}
