package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/presence"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type GroupDetails struct {
	GroupSummary
	QuotaBytes       int64                `json:"quotaBytes" example:"107374182400"`
	OnlineCount      int                  `json:"onlineCount" example:"3"`
	ExpiringCount    int                  `json:"expiringCount" example:"2"`
	InboundIDs       []int                `json:"inboundIds" example:"[1,2]"`
	SharedInboundIDs []int                `json:"sharedInboundIds" example:"[1,2]"`
	History          []ClientHistoryPoint `json:"history"`
}

type GroupQuotaRequest struct {
	Name       string `json:"name" binding:"required" example:"Family"`
	QuotaBytes int64  `json:"quotaBytes" binding:"min=0" example:"107374182400"`
	InboundIDs *[]int `json:"inboundIds,omitempty" example:"[1,2]"`
}

func (s *ClientService) SetGroupQuota(req GroupQuotaRequest) error {
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" || req.QuotaBytes < 0 {
		return errors.New("group name and a nonnegative quota are required")
	}
	if req.InboundIDs != nil {
		if err := s.setGroupInbounds(req.Name, *req.InboundIDs, req.QuotaBytes); err != nil {
			return err
		}
	} else if err := database.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := requireLegacyGroupEditTx(tx, req.Name); err != nil {
			return err
		}
		return saveLegacyGroupQuotaTx(tx, req.Name, req.QuotaBytes)
	}); err != nil {
		return err
	}
	return s.EnforceGroupQuotas()
}

func requireLegacyGroupEditTx(tx *gorm.DB, name string) error {
	var group model.ClientGroup
	if err := tx.Where("name = ?", name).First(&group).Error; err != nil {
		return err
	}
	if err := lockLegacyGroupMutationTx(tx, nil, []string{name}, ""); err != nil {
		return err
	}
	return tx.Where("id = ? AND name = ?", group.Id, name).First(&model.ClientGroup{}).Error
}

func saveLegacyGroupQuotaTx(tx *gorm.DB, name string, quotaBytes int64) error {
	return tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{"quota_bytes"}),
	}).
		Create(&model.GroupQuota{Name: name, QuotaBytes: quotaBytes}).Error
}

func (s *ClientService) ListGroupDetails() ([]GroupDetails, error) {
	groups, err := listManagedGroupSummaries()
	if err != nil {
		return nil, err
	}
	db := database.GetDB()
	var quotas []model.GroupQuota
	if err := db.Find(&quotas).Error; err != nil {
		return nil, err
	}
	quotaByName := map[string]int64{}
	for _, q := range quotas {
		quotaByName[q.Name] = q.QuotaBytes
	}
	var clients []struct {
		Email, GroupName string
		ExpiryTime       int64
	}
	if err := db.Model(&model.ClientRecord{}).Select("email, group_name, expiry_time").
		Where("group_name <> '' AND owner_user_id IS NULL").Scan(&clients).Error; err != nil {
		return nil, err
	}
	var attachments []struct {
		GroupName string
		InboundID int
	}
	if err := db.Table("clients c").Select("DISTINCT c.group_name, ci.inbound_id").
		Joins("JOIN client_inbounds ci ON ci.client_id = c.id").Where("c.group_name <> '' AND c.owner_user_id IS NULL").
		Order("ci.inbound_id").Scan(&attachments).Error; err != nil {
		return nil, err
	}
	online := map[string]bool{}
	for _, email := range presence.Shared().Online() {
		online[email] = true
	}
	out := make([]GroupDetails, len(groups))
	byName := map[string]*GroupDetails{}
	for i, g := range groups {
		out[i] = GroupDetails{GroupSummary: g, QuotaBytes: quotaByName[g.Name], InboundIDs: []int{}, SharedInboundIDs: []int{}, History: []ClientHistoryPoint{}}
		byName[g.Name] = &out[i]
	}
	var shared []model.GroupInbound
	if err := db.Order("inbound_id").Find(&shared).Error; err != nil {
		return nil, err
	}
	for _, link := range shared {
		if g := byName[link.Name]; g != nil {
			g.SharedInboundIDs = append(g.SharedInboundIDs, link.InboundID)
		}
	}
	now := time.Now()
	for _, c := range clients {
		g := byName[c.GroupName]
		if g == nil {
			continue
		}
		if online[c.Email] {
			g.OnlineCount++
		}
		if c.ExpiryTime > now.UnixMilli() && c.ExpiryTime <= now.Add(7*24*time.Hour).UnixMilli() {
			g.ExpiringCount++
		}
	}
	for _, a := range attachments {
		if g := byName[a.GroupName]; g != nil {
			g.InboundIDs = append(g.InboundIDs, a.InboundID)
		}
	}
	var history []struct {
		GroupName          string
		ClientHistoryPoint `gorm:"embedded"`
	}
	since := now.Unix()/86400*86400 - 13*86400
	err = db.Raw(`WITH reported AS (
SELECT c.group_name,h.t/86400*86400 AS t,h.up,h.down FROM client_traffic_samples h JOIN clients c ON c.email=h.email
WHERE c.group_name<>'' AND c.owner_user_id IS NULL AND h.t>=?
AND NOT EXISTS (SELECT 1 FROM fleet_clients fc WHERE fc.panel_client_id=c.id AND fc.owned)
UNION ALL
SELECT g.name,h.day,h.uploaded,h.downloaded FROM fleet_term_usage_days h
JOIN fleet_term_groups p ON p.term_id=h.term_id JOIN client_groups g ON g.id=p.group_id
JOIN fleet_terms t ON t.id=h.term_id JOIN fleet_clients fc ON fc.id=t.client_id AND fc.owned WHERE h.day>=?
) SELECT group_name,t,SUM(up) AS up,SUM(down) AS down FROM reported GROUP BY group_name,t ORDER BY t`, since, since).Scan(&history).Error
	if err != nil {
		return nil, err
	}
	for _, row := range history {
		if g := byName[row.GroupName]; g != nil {
			g.History = append(g.History, row.ClientHistoryPoint)
		}
	}
	return out, nil
}

// EnforceGroupQuotas disables members through the bulk service's Runtime dispatch.
// Raising/resetting a pool does not silently re-enable manually disabled clients.
func (s *ClientService) EnforceGroupQuotas() error {
	groups, err := listManagedGroupSummaries()
	if err != nil {
		return err
	}
	db := database.GetDB()
	var quotas []model.GroupQuota
	if err := db.Where("quota_bytes > 0").Find(&quotas).Error; err != nil {
		return err
	}
	used := map[string]int64{}
	for _, g := range groups {
		used[g.Name] = g.TrafficUsed
	}
	var exhausted []string
	for _, q := range quotas {
		if used[q.Name] >= q.QuotaBytes {
			exhausted = append(exhausted, q.Name)
		}
	}
	if len(exhausted) == 0 {
		return nil
	}
	var emails []string
	if err := db.Model(&model.ClientRecord{}).Where("group_name IN ? AND owner_user_id IS NULL AND enable = TRUE", exhausted).Pluck("email", &emails).Error; err != nil {
		return err
	}
	result, restart, err := s.BulkSetEnable(&InboundService{}, emails, false)
	if restart {
		(&XrayService{}).SetToNeedRestart()
	}
	if err != nil {
		return err
	}
	if result.Changed > 0 {
		if _, err := (ActivityService{}).Record(context.Background(), model.ActivityEvent{
			AudienceUserID: 0, Kind: "group.quota", Level: "warning", TitleKey: "telemetry.groupQuotaReached", MessageKey: "telemetry.groupQuotaDisabled",
		}, map[string]string{"count": strconv.Itoa(result.Changed)}); err != nil {
			return err
		}
	}
	if len(result.Skipped) > 0 {
		return fmt.Errorf("group quota enforcement: %s: %s", result.Skipped[0].Email, result.Skipped[0].Reason)
	}
	return nil
}

func listManagedGroupSummaries() ([]GroupSummary, error) {
	return readFleetGroupSummaries(database.GetDB(), true)
}
