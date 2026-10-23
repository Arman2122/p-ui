package service

import (
	"encoding/json"
	"strings"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/util/common"

	"gorm.io/gorm"
)

type GroupSummary struct {
	Name        string `json:"name" example:"Family"`
	ClientCount int    `json:"clientCount" example:"3"`
	TrafficUsed int64  `json:"trafficUsed" example:"10485760"`
	Up          int64  `json:"up" example:"1048576"`
	Down        int64  `json:"down" example:"9437184"`
}

func (s *ClientService) ListGroups() ([]GroupSummary, error) {
	return readFleetGroupSummaries(database.GetDB(), false)
}

// adjustGroupBaselinesForRemovedTraffic shifts group baselines down by the clients'
// current counters so ListGroups totals survive a traffic reset or client delete (#5675).
func adjustGroupBaselinesForRemovedTraffic(tx *gorm.DB, emails []string) error {
	if len(emails) == 0 {
		return nil
	}
	type groupDelta struct {
		Name string
		Up   int64
		Down int64
	}
	totals := make(map[string]*groupDelta)
	for _, batch := range chunkStrings(emails, sqlInChunk) {
		var part []groupDelta
		if err := tx.Table("clients AS c").
			Select("c.group_name AS name, COALESCE(SUM(ct.up), 0) AS up, COALESCE(SUM(ct.down), 0) AS down").
			Joins("JOIN client_traffics ct ON ct.email = c.email").
			Where("c.group_name <> '' AND c.email IN ?", batch).
			Where("NOT EXISTS (SELECT 1 FROM fleet_clients fc WHERE fc.panel_client_id=c.id AND fc.owned)").
			Group("c.group_name").
			Scan(&part).Error; err != nil {
			return err
		}
		for i := range part {
			if agg, ok := totals[part[i].Name]; ok {
				agg.Up += part[i].Up
				agg.Down += part[i].Down
			} else {
				totals[part[i].Name] = &part[i]
			}
		}
	}
	for name, d := range totals {
		if d.Up == 0 && d.Down == 0 {
			continue
		}
		res := tx.Model(&model.ClientGroup{}).Where("name = ?", name).Updates(map[string]any{
			"reset_up":   gorm.Expr("reset_up - ?", d.Up),
			"reset_down": gorm.Expr("reset_down - ?", d.Down),
		})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			if err := tx.Create(&model.ClientGroup{Name: name, ResetUp: -d.Up, ResetDown: -d.Down}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *ClientService) EmailsByGroup(name string) ([]string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return []string{}, nil
	}
	db := database.GetDB()
	var emails []string
	if err := db.Model(&model.ClientRecord{}).
		Where("group_name = ?", name).
		Order("email ASC").
		Pluck("email", &emails).Error; err != nil {
		return nil, err
	}
	if emails == nil {
		emails = []string{}
	}
	return emails, nil
}

func (s *ClientService) ResetGroupTraffic(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return common.NewError("group name is required")
	}
	return submitTrafficWrite(func() error {
		return database.GetDB().Transaction(func(db *gorm.DB) error {
			if err := requireLegacyPool(db, nil, name); err != nil {
				return err
			}
			var agg struct {
				Up   int64
				Down int64
			}
			if err := db.Table("clients AS c").
				Select("COALESCE(SUM(ct.up), 0) AS up, COALESCE(SUM(ct.down), 0) AS down").
				Joins("LEFT JOIN client_traffics ct ON ct.email = c.email").
				Where("c.group_name = ?", name).
				Scan(&agg).Error; err != nil {
				return err
			}
			var count int64
			if err := db.Model(&model.ClientGroup{}).Where("name = ?", name).Count(&count).Error; err != nil {
				return err
			}
			if count == 0 {
				return db.Create(&model.ClientGroup{Name: name, ResetUp: agg.Up, ResetDown: agg.Down}).Error
			}
			return db.Model(&model.ClientGroup{}).Where("name = ?", name).
				Updates(map[string]any{"reset_up": agg.Up, "reset_down": agg.Down}).Error
		})
	})
}

func (s *ClientService) CreateGroup(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return common.NewError("group name is required")
	}
	db := database.GetDB()
	var count int64
	if err := db.Model(&model.ClientGroup{}).Where("name = ?", name).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return common.NewError("group already exists")
	}
	return db.Create(&model.ClientGroup{Name: name}).Error
}

func (s *ClientService) RenameGroup(oldName, newName string) (int, error) {
	oldName = strings.TrimSpace(oldName)
	newName = strings.TrimSpace(newName)
	if oldName == "" {
		return 0, common.NewError("old group name is required")
	}
	if newName == "" {
		return 0, common.NewError("new group name is required")
	}
	if oldName == newName {
		return 0, nil
	}
	return s.replaceGroupValue(oldName, newName)
}

func (s *ClientService) DeleteGroup(name string) (int, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return 0, common.NewError("group name is required")
	}
	return s.replaceGroupValue(name, "")
}

func (s *ClientService) RemoveFromGroup(emails []string) (int, error) {
	return s.AddToGroup(emails, "")
}

func (s *ClientService) AddToGroup(emails []string, group string) (int, error) {
	group = strings.TrimSpace(group)
	if len(emails) == 0 {
		return 0, nil
	}
	db := database.GetDB().Begin()
	if db.Error != nil {
		return 0, db.Error
	}
	defer db.Rollback()
	if err := lockLegacyGroupMutationTx(db, emails, []string{group}, group); err != nil {
		return 0, err
	}
	if group != "" {
		for _, batch := range chunkStrings(emails, sqlInChunk) {
			var owned int64
			if err := db.Model(&model.ClientRecord{}).Where("email IN ? AND owner_user_id IS NOT NULL", batch).Count(&owned).Error; err != nil {
				return 0, err
			}
			if owned > 0 {
				return 0, ErrResellerForbidden
			}
		}
	}

	if group != "" {
		var exists int64
		if err := db.Model(&model.ClientGroup{}).Where("name = ?", group).Count(&exists).Error; err != nil {
			return 0, err
		}
		if exists == 0 {
			var derived int64
			if err := db.Model(&model.ClientRecord{}).Where("group_name = ?", group).Count(&derived).Error; err != nil {
				return 0, err
			}
			if derived == 0 {
				if err := db.Create(&model.ClientGroup{Name: group}).Error; err != nil {
					return 0, err
				}
			}
		}
	}

	var records []model.ClientRecord
	for _, batch := range chunkStrings(emails, sqlInChunk) {
		var rows []model.ClientRecord
		if err := db.Where("email IN ?", batch).
			Where("group_name IS NULL OR group_name <> ?", group).Find(&rows).Error; err != nil {
			return 0, err
		}
		records = append(records, rows...)
	}
	if len(records) == 0 {
		return 0, db.Commit().Error
	}
	affectedEmails := make([]string, 0, len(records))
	for _, r := range records {
		affectedEmails = append(affectedEmails, r.Email)
	}

	tx := db
	if err := preserveGroupTransfer(tx, affectedEmails, group); err != nil {
		tx.Rollback()
		return 0, err
	}
	var affected int64
	for _, batch := range chunkStrings(affectedEmails, sqlInChunk) {
		result := tx.Model(&model.ClientRecord{}).
			Where("email IN ?", batch).
			Where("group_name IS NULL OR group_name <> ?", group).
			UpdateColumn("group_name", group)
		if result.Error != nil {
			tx.Rollback()
			return 0, result.Error
		}
		affected += result.RowsAffected
	}

	var inboundIDs []int
	inboundIDSeen := make(map[int]struct{})
	for _, batch := range chunkStrings(affectedEmails, sqlInChunk) {
		var ids []int
		if err := tx.Table("client_inbounds").
			Joins("JOIN clients ON clients.id = client_inbounds.client_id").
			Where("clients.email IN ?", batch).
			Distinct("client_inbounds.inbound_id").
			Pluck("inbound_id", &ids).Error; err != nil {
			tx.Rollback()
			return 0, err
		}
		for _, id := range ids {
			if _, ok := inboundIDSeen[id]; !ok {
				inboundIDSeen[id] = struct{}{}
				inboundIDs = append(inboundIDs, id)
			}
		}
	}

	emailSet := make(map[string]struct{}, len(affectedEmails))
	for _, e := range affectedEmails {
		emailSet[e] = struct{}{}
	}

	for _, ibID := range inboundIDs {
		var ib model.Inbound
		if err := tx.First(&ib, ibID).Error; err != nil {
			tx.Rollback()
			return 0, err
		}
		var settings map[string]any
		if err := json.Unmarshal([]byte(ib.Settings), &settings); err != nil {
			continue
		}
		clients, ok := settings["clients"].([]any)
		if !ok {
			continue
		}
		modified := false
		for i := range clients {
			cm, ok := clients[i].(map[string]any)
			if !ok {
				continue
			}
			email, _ := cm["email"].(string)
			if _, hit := emailSet[email]; !hit {
				continue
			}
			if group == "" {
				delete(cm, "group")
			} else {
				cm["group"] = group
			}
			clients[i] = cm
			modified = true
		}
		if modified {
			settings["clients"] = clients
			newSettings, err := json.Marshal(settings)
			if err != nil {
				continue
			}
			ib.Settings = string(newSettings)
			if err := tx.Save(&ib).Error; err != nil {
				tx.Rollback()
				return 0, err
			}
		}
	}

	if err := tx.Commit().Error; err != nil {
		return 0, err
	}
	return int(affected), nil
}

func (s *ClientService) replaceGroupValue(oldName, newName string) (int, error) {
	db := database.GetDB().Begin()
	if db.Error != nil {
		return 0, db.Error
	}
	defer db.Rollback()
	var exists bool
	if err := db.Raw("SELECT EXISTS (SELECT 1 FROM client_groups WHERE name=?) OR EXISTS (SELECT 1 FROM clients WHERE group_name=?)", oldName, oldName).Scan(&exists).Error; err != nil {
		return 0, err
	}
	if !exists {
		return 0, db.Commit().Error
	}
	if err := lockLegacyGroupMutationTx(db, nil, []string{oldName, newName}, ""); err != nil {
		return 0, err
	}
	if newName == "" {
		if err := db.Where("name = ?", oldName).Delete(&model.ClientGroup{}).Error; err != nil {
			return 0, err
		}
	} else {
		if err := db.Model(&model.ClientGroup{}).Where("name = ?", oldName).Update("name", newName).Error; err != nil {
			return 0, err
		}
	}
	var records []model.ClientRecord
	if err := db.Where("group_name = ?", oldName).Find(&records).Error; err != nil {
		return 0, err
	}
	if len(records) == 0 {
		return 0, db.Commit().Error
	}
	affectedEmails := make([]string, 0, len(records))
	for _, r := range records {
		affectedEmails = append(affectedEmails, r.Email)
	}

	tx := db
	if err := tx.Model(&model.ClientRecord{}).
		Where("group_name = ?", oldName).
		UpdateColumn("group_name", newName).Error; err != nil {
		tx.Rollback()
		return 0, err
	}

	var inboundIDs []int
	inboundIDSeen := make(map[int]struct{})
	for _, batch := range chunkStrings(affectedEmails, sqlInChunk) {
		var ids []int
		if err := tx.Table("client_inbounds").
			Joins("JOIN clients ON clients.id = client_inbounds.client_id").
			Where("clients.email IN ?", batch).
			Distinct("client_inbounds.inbound_id").
			Pluck("inbound_id", &ids).Error; err != nil {
			tx.Rollback()
			return 0, err
		}
		for _, id := range ids {
			if _, ok := inboundIDSeen[id]; !ok {
				inboundIDSeen[id] = struct{}{}
				inboundIDs = append(inboundIDs, id)
			}
		}
	}

	for _, ibID := range inboundIDs {
		var ib model.Inbound
		if err := tx.First(&ib, ibID).Error; err != nil {
			tx.Rollback()
			return 0, err
		}
		var settings map[string]any
		if err := json.Unmarshal([]byte(ib.Settings), &settings); err != nil {
			continue
		}
		clients, ok := settings["clients"].([]any)
		if !ok {
			continue
		}
		modified := false
		for i := range clients {
			cm, ok := clients[i].(map[string]any)
			if !ok {
				continue
			}
			if g, ok := cm["group"].(string); ok && g == oldName {
				if newName == "" {
					delete(cm, "group")
				} else {
					cm["group"] = newName
				}
				clients[i] = cm
				modified = true
			}
		}
		if modified {
			settings["clients"] = clients
			newSettings, err := json.Marshal(settings)
			if err != nil {
				continue
			}
			ib.Settings = string(newSettings)
			if err := tx.Save(&ib).Error; err != nil {
				tx.Rollback()
				return 0, err
			}
		}
	}

	if err := tx.Commit().Error; err != nil {
		return 0, err
	}
	return len(records), nil
}

// BulkGroupResult is the per-row account of a bulk group change.
type BulkGroupResult struct {
	Affected int         `json:"affected" example:"2"`
	Outcome  BulkOutcome `json:"outcome"`
}

// AddToGroupOutcome groups like AddToGroup and names the emails it could not
// find, which the count alone folded into "fewer than asked".
func (s *ClientService) AddToGroupOutcome(emails []string, group string) (*BulkGroupResult, error) {
	result := &BulkGroupResult{}
	known := map[string]struct{}{}
	for _, batch := range chunkStrings(emails, sqlInChunk) {
		var found []string
		if err := database.GetDB().Model(&model.ClientRecord{}).Where("email IN ?", batch).Pluck("email", &found).Error; err != nil {
			return nil, err
		}
		for _, email := range found {
			known[email] = struct{}{}
		}
	}
	affected, err := s.AddToGroup(emails, group)
	if err != nil {
		return nil, err
	}
	result.Affected = affected
	seen := map[string]struct{}{}
	for _, email := range emails {
		if email == "" {
			continue
		}
		if _, dup := seen[email]; dup {
			continue
		}
		seen[email] = struct{}{}
		if _, ok := known[email]; ok {
			result.Outcome.ok(email)
		} else {
			result.Outcome.skip(email, "client not found")
		}
	}
	result.Outcome.finalize()
	return result, nil
}
