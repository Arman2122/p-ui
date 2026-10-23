package service

import (
	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database/model"
)

const legacyTrafficOwnerSQL = `NOT EXISTS (SELECT 1 FROM fleet_clients fleet_owner JOIN clients fleet_client ON fleet_client.id = fleet_owner.panel_client_id WHERE fleet_owner.owned AND fleet_client.email = client_traffics.email)`

func requireLegacyClientLifecycle(tx *gorm.DB, emails []string) error {
	if len(emails) == 0 {
		return nil
	}
	for _, chunk := range chunkStrings(emails, sqlInChunk) {
		var count int64
		if err := tx.Model(&model.FleetClient{}).Joins("JOIN clients c ON c.id = fleet_clients.panel_client_id").Where("fleet_clients.owned AND c.email IN ?", chunk).Count(&count).Error; err != nil {
			return err
		}
		if count != 0 {
			return ErrFleetLifecycle
		}
	}
	return nil
}

func requireLegacyPool(tx *gorm.DB, ownerID *int, group string) error {
	query := tx.Model(&model.FleetClient{}).Joins("JOIN clients c ON c.id = fleet_clients.panel_client_id").Where("fleet_clients.owned")
	if ownerID != nil {
		query = query.Where("c.owner_user_id = ?", *ownerID)
	} else {
		query = query.Where("c.owner_user_id IS NULL AND c.group_name = ?", group)
	}
	var count int64
	if err := query.Count(&count).Error; err != nil {
		return err
	}
	if count != 0 {
		return ErrFleetLifecycle
	}
	if ownerID == nil && group != "" {
		var historical bool
		if err := tx.Raw(`SELECT EXISTS (SELECT 1 FROM client_groups g WHERE g.name=? AND (
EXISTS (SELECT 1 FROM fleet_term_groups p WHERE p.group_id=g.id)
OR EXISTS (SELECT 1 FROM fleet_budget_scopes s WHERE s.kind='group' AND s.source_id=g.id)))`, group).Scan(&historical).Error; err != nil {
			return err
		}
		if historical {
			return ErrFleetLifecycle
		}
	}
	return nil
}
