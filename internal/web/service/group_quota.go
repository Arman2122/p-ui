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
	db := database.GetDB()
	var group model.ClientGroup
	if err := db.Where("name = ?", req.Name).First(&group).Error; err != nil {
		return err
	}
	if req.InboundIDs != nil {
		if err := s.setGroupInbounds(req.Name, *req.InboundIDs); err != nil {
			return err
		}
	}
	if err := db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "name"}},
		DoUpdates: clause.AssignmentColumns([]string{"quota_bytes"}),
	}).
		Create(&model.GroupQuota{Name: req.Name, QuotaBytes: req.QuotaBytes}).Error; err != nil {
		return err
	}
	return s.EnforceGroupQuotas()
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
	err = db.Raw(`SELECT c.group_name, h.t / 86400 * 86400 AS t, SUM(h.up) AS up, SUM(h.down) AS down
		FROM client_traffic_samples h JOIN clients c ON c.email = h.email
		WHERE c.group_name <> '' AND c.owner_user_id IS NULL AND h.t >= ? GROUP BY c.group_name, h.t / 86400 ORDER BY t`, since).Scan(&history).Error
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
	rows := []GroupSummary{}
	err := database.GetDB().Raw(`WITH names AS (
		SELECT name FROM client_groups UNION SELECT group_name FROM clients WHERE group_name <> '' AND owner_user_id IS NULL
	) SELECT names.name, COUNT(c.id) AS client_count,
	GREATEST(COALESCE(SUM(ct.up), 0) - COALESCE(g.reset_up, 0), 0) AS up,
	GREATEST(COALESCE(SUM(ct.down), 0) - COALESCE(g.reset_down, 0), 0) AS down
	FROM names LEFT JOIN client_groups g ON g.name = names.name
	LEFT JOIN clients c ON c.group_name = names.name AND c.owner_user_id IS NULL
	LEFT JOIN client_traffics ct ON ct.email = c.email
	GROUP BY names.name, g.reset_up, g.reset_down ORDER BY LOWER(names.name)`).Scan(&rows).Error
	for i := range rows {
		rows[i].TrafficUsed = rows[i].Up + rows[i].Down
	}
	return rows, err
}
