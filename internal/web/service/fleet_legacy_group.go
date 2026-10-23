package service

import (
	"slices"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func observeLegacyGroupSources(tx *gorm.DB, emails, groups []string) ([]model.ClientRecord, error) {
	var records []model.ClientRecord
	for _, batch := range chunkStrings(emails, sqlInChunk) {
		var rows []model.ClientRecord
		if err := tx.Where("email IN ?", batch).Find(&rows).Error; err != nil {
			return nil, err
		}
		records = append(records, rows...)
	}
	if len(groups) != 0 {
		var rows []model.ClientRecord
		if err := tx.Where("group_name IN ?", groups).Find(&rows).Error; err != nil {
			return nil, err
		}
		records = append(records, rows...)
	}
	return records, nil
}

func lockLegacySourceRows(tx *gorm.DB, observed []model.ClientRecord) (map[int]model.ClientRecord, error) {
	expected := make(map[int]model.ClientRecord, len(observed))
	for _, row := range observed {
		expected[row.Id] = row
	}
	ids := make([]int, 0, len(expected))
	for id := range expected {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, batch := range chunkInts(ids, sqlInChunk) {
		var locked []model.ClientRecord
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ?", batch).Order("id").Find(&locked).Error; err != nil {
			return nil, err
		}
		if len(locked) != len(batch) {
			return nil, ErrFleetSource
		}
		if err := requireLockedLegacySources(expected, locked); err != nil {
			return nil, err
		}
	}
	return expected, nil
}

func requireLockedLegacySources(locked map[int]model.ClientRecord, current []model.ClientRecord) error {
	for _, row := range current {
		previous, exists := locked[row.Id]
		if !exists || previous.Email != row.Email || previous.Group != row.Group {
			return ErrFleetSource
		}
	}
	return nil
}

func lockLegacyGroupMutationTx(tx *gorm.DB, emails, groups []string, createGroup string) error {
	observed, err := observeLegacyGroupSources(tx, emails, nil)
	if err != nil {
		return err
	}
	groups = slices.Clone(groups)
	ensure := []string{}
	for _, row := range observed {
		groups = append(groups, row.Group)
	}
	groups = append(groups, createGroup)
	slices.Sort(groups)
	groups = slices.DeleteFunc(slices.Compact(groups), func(name string) bool { return name == "" })
	grouped, err := observeLegacyGroupSources(tx, nil, groups)
	if err != nil {
		return err
	}
	observed = append(observed, grouped...)
	for _, row := range observed {
		ensure = append(ensure, row.Group)
	}
	locked, err := lockLegacySourceRows(tx, observed)
	if err != nil {
		return err
	}
	ensure = append(ensure, createGroup)
	slices.Sort(ensure)
	for _, name := range slices.Compact(ensure) {
		if name != "" {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.ClientGroup{Name: name}).Error; err != nil {
				return err
			}
		}
	}
	if len(groups) != 0 {
		var parents []model.ClientGroup
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("name IN ?", groups).Order("id").Find(&parents).Error; err != nil {
			return err
		}
	}
	current, err := observeLegacyGroupSources(tx, emails, groups)
	if err != nil {
		return err
	}
	if err := requireLockedLegacySources(locked, current); err != nil {
		return err
	}
	selected := make([]string, 0, len(locked))
	for _, row := range locked {
		selected = append(selected, row.Email)
	}
	if err := requireLegacyClientLifecycle(tx, selected); err != nil {
		return err
	}
	for _, group := range groups {
		if err := requireLegacyPool(tx, nil, group); err != nil {
			return err
		}
	}
	return nil
}

func observeLegacyInboundSources(tx *gorm.DB, inbound model.Inbound, incoming []model.Client) ([]model.ClientRecord, error) {
	clients, err := (&InboundService{}).GetClients(&inbound)
	if err != nil {
		return nil, err
	}
	emails := clientEmails(append(clients, incoming...))
	records, err := observeLegacyGroupSources(tx, emails, nil)
	if err != nil {
		return nil, err
	}
	var linked []model.ClientRecord
	if err := tx.Model(&model.ClientRecord{}).Joins("JOIN client_inbounds ci ON ci.client_id=clients.id").Where("ci.inbound_id = ?", inbound.Id).Find(&linked).Error; err != nil {
		return nil, err
	}
	return append(records, linked...), nil
}

func lockLegacyInboundMutationTx(tx *gorm.DB, inboundID int, incoming []model.Client) error {
	var inbound model.Inbound
	if err := tx.First(&inbound, inboundID).Error; err != nil {
		return err
	}
	observed, err := observeLegacyInboundSources(tx, inbound, incoming)
	if err != nil {
		return err
	}
	locked, err := lockLegacySourceRows(tx, observed)
	if err != nil {
		return err
	}
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&inbound, inboundID).Error; err != nil {
		return err
	}
	current, err := observeLegacyInboundSources(tx, inbound, incoming)
	if err != nil {
		return err
	}
	if err := requireLockedLegacySources(locked, current); err != nil {
		return err
	}
	emails := make([]string, 0, len(current))
	for _, row := range current {
		emails = append(emails, row.Email)
	}
	return requireLegacyClientLifecycle(tx, emails)
}
