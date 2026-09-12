package service

import (
	"errors"
	"slices"
	"strings"
	"sync"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var groupMembershipMu sync.Mutex

// Moving membership preserves the old pool's consumption and excludes bytes
// carried into the new pool from before the client joined it.
func preserveGroupTransfer(tx *gorm.DB, emails []string, target string) error {
	var up, down int64
	for _, batch := range chunkStrings(emails, sqlInChunk) {
		var changed []string
		if err := tx.Model(&model.ClientRecord{}).Where("email IN ? AND COALESCE(group_name, '') <> ?", batch, target).Pluck("email", &changed).Error; err != nil {
			return err
		}
		if len(changed) == 0 {
			continue
		}
		var usage struct{ Up, Down int64 }
		if err := tx.Table("client_traffics").Select("COALESCE(SUM(up), 0) AS up, COALESCE(SUM(down), 0) AS down").Where("email IN ?", changed).Scan(&usage).Error; err != nil {
			return err
		}
		if err := adjustGroupBaselinesForRemovedTraffic(tx, changed); err != nil {
			return err
		}
		up, down = up+usage.Up, down+usage.Down
	}
	if target == "" || up+down == 0 {
		return nil
	}
	return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "name"}}, DoUpdates: clause.Assignments(map[string]any{
		"reset_up":   gorm.Expr("client_groups.reset_up + EXCLUDED.reset_up"),
		"reset_down": gorm.Expr("client_groups.reset_down + EXCLUDED.reset_down"),
	})}).Create(&model.ClientGroup{Name: target, ResetUp: up, ResetDown: down}).Error
}

func (s *ClientService) setGroupInbounds(name string, ids []int) error {
	groupMembershipMu.Lock()
	defer groupMembershipMu.Unlock()
	db := database.GetDB()
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	var count int64
	if len(ids) > 0 {
		if err := db.Model(&model.Inbound{}).Where("id IN ?", ids).Count(&count).Error; err != nil {
			return err
		}
		if int(count) != len(ids) {
			return errors.New("one or more group inbounds do not exist")
		}
	}
	var previous []int
	if err := db.Model(&model.GroupInbound{}).Where("name = ?", name).Pluck("inbound_id", &previous).Error; err != nil {
		return err
	}
	emails, err := s.EmailsByGroup(name)
	if err != nil {
		return err
	}
	removed := []int{}
	for _, id := range previous {
		if !slices.Contains(ids, id) {
			removed = append(removed, id)
		}
	}
	if len(emails) > 0 {
		result, restart, err := s.BulkAttach(&InboundService{}, emails, ids)
		if restart {
			(&XrayService{}).SetToNeedRestart()
		}
		if err != nil {
			return err
		}
		if len(result.Errors) > 0 {
			return errors.New(strings.Join(result.Errors, "; "))
		}
		if len(removed) > 0 {
			result, restart, err := s.BulkDetach(&InboundService{}, emails, removed)
			if restart {
				(&XrayService{}).SetToNeedRestart()
			}
			if err != nil {
				return err
			}
			if len(result.Errors) > 0 {
				return errors.New(strings.Join(result.Errors, "; "))
			}
		}
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("name = ?", name).Delete(&model.GroupInbound{}).Error; err != nil {
			return err
		}
		rows := make([]model.GroupInbound, 0, len(ids))
		for _, id := range ids {
			rows = append(rows, model.GroupInbound{Name: name, InboundID: id})
		}
		if len(rows) == 0 {
			return nil
		}
		return tx.Create(&rows).Error
	})
}

func (s *ClientService) ApplyGroupInbounds(name string) error {
	if strings.TrimSpace(name) == "" {
		return nil
	}
	return s.applyMissingGroupInbounds(strings.TrimSpace(name))
}

func (s *ClientService) ConvergeGroupMemberships() error {
	return s.applyMissingGroupInbounds("")
}

func (s *ClientService) applyMissingGroupInbounds(name string) error {
	groupMembershipMu.Lock()
	defer groupMembershipMu.Unlock()
	var missing []struct {
		Email     string
		InboundID int
	}
	query := database.GetDB().Table("group_inbounds gi").Select("c.email, gi.inbound_id").
		Joins("JOIN clients c ON c.group_name = gi.name AND c.owner_user_id IS NULL").
		Joins("LEFT JOIN client_inbounds ci ON ci.client_id = c.id AND ci.inbound_id = gi.inbound_id").Where("ci.client_id IS NULL")
	if name != "" {
		query = query.Where("gi.name = ?", name)
	}
	if err := query.Scan(&missing).Error; err != nil {
		return err
	}
	byInbound := map[int][]string{}
	for _, pair := range missing {
		byInbound[pair.InboundID] = append(byInbound[pair.InboundID], pair.Email)
	}
	var failures []error
	for id, emails := range byInbound {
		result, restart, err := s.BulkAttach(&InboundService{}, emails, []int{id})
		if restart {
			(&XrayService{}).SetToNeedRestart()
		}
		if err != nil {
			failures = append(failures, err)
		} else if len(result.Errors) > 0 {
			failures = append(failures, errors.New(strings.Join(result.Errors, "; ")))
		}
	}
	return errors.Join(failures...)
}
