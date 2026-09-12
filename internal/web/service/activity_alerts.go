package service

import (
	"context"
	"strconv"
	"time"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func (s ActivityService) Alerts(ctx context.Context, userID int, admin, secure bool) ([]ActivityView, error) {
	alerts := []ActivityView{}
	add := func(id, level, key string, params map[string]string) {
		alerts = append(alerts, ActivityView{
			ID: "alert:" + id, Kind: id, Category: "alert", Level: level,
			TitleKey: "activity.alerts." + key + ".title", MessageKey: "activity.alerts." + key + ".body", Params: params, Read: true,
		})
	}
	if !admin {
		var account model.ResellerAccount
		if err := database.GetDB().WithContext(ctx).Where("user_id = ?", userID).First(&account).Error; err != nil {
			return nil, err
		}
		if account.Suspended {
			add("account-suspended", "error", "accountSuspended", map[string]string{})
		}
		if account.NotifyOnDepletion && account.TrafficLimit > 0 && account.TrafficUsed >= account.TrafficLimit {
			add("account-budget", "warning", "accountBudget", map[string]string{})
		} else if account.NotifyOnDepletion && account.TrafficLimit > 0 && account.TrafficUsed >= account.TrafficLimit-account.TrafficLimit/10 {
			add("account-budget", "warning", "accountBudgetSoon", map[string]string{})
		}
		if account.NotifyOnDepletion && account.ExpiresAt > 0 && account.ExpiresAt <= time.Now().Add(14*24*time.Hour).UnixMilli() {
			add("account-expiry", "warning", "accountExpiry", map[string]string{})
		}
		return alerts, nil
	}
	if !secure {
		add("http", "warning", "http", map[string]string{})
	}
	for _, core := range CoreStatusViews(ctx) {
		if core.Available && (core.State == "failed" || core.State == "degraded" || (core.State == "running" && !core.Ready)) {
			add("core-"+core.ID, "error", "core", map[string]string{"core": core.ID})
		}
	}
	var nodes []model.Node
	if err := database.GetDB().WithContext(ctx).Select("id", "name").Where("enable = ? AND status = ?", true, "offline").Find(&nodes).Error; err != nil {
		return nil, err
	}
	for _, node := range nodes {
		add("node-"+strconv.Itoa(node.Id), "error", "node", map[string]string{"node": node.Name})
	}
	settings := SettingService{}
	days, err := settings.GetExpireDiff()
	if err != nil {
		return nil, err
	}
	if days > 0 {
		var count int64
		now := time.Now()
		if err := database.GetDB().WithContext(ctx).Model(&model.ClientRecord{}).Where("enable = ? AND expiry_time > ? AND expiry_time <= ?", true, now.UnixMilli(), now.Add(time.Duration(days)*24*time.Hour).UnixMilli()).Count(&count).Error; err != nil {
			return nil, err
		}
		if count > 0 {
			add("clients-expiring", "warning", "clients", map[string]string{"count": strconv.FormatInt(count, 10), "days": strconv.Itoa(days)})
		}
	}
	return alerts, nil
}
