package service

import (
	"encoding/json"
	"fmt"

	"github.com/Arman2122/p-ui/internal/core"
)

func mergeTrackedNodeLifecycle(master core.ClientTraffic, incoming core.ClientTraffic, base nodeTrafficCounter, seen, frozen bool, settingsExpiry *int64, now, deltaUp, deltaDown int64) (core.ClientTraffic, bool, bool, error) {
	if frozen {
		return master, false, false, nil
	}
	if incoming.ResetCount == nil || *incoming.ResetCount < 0 {
		return master, false, false, fmt.Errorf("invalid renewal generation for %q", incoming.Email)
	}
	previous := int64(0)
	if base.ResetCount != nil {
		previous = *base.ResetCount
	}
	if settingsExpiry != nil && *settingsExpiry != incoming.ExpiryTime {
		return master, false, false, nil
	}
	if *incoming.ResetCount < previous {
		return master, false, true, nil
	}
	legacyTransition := seen && base.ResetCount == nil && base.ObservedResetCount == nil && *incoming.ResetCount == 0 && master.ExpiryTime > 0 && master.ExpiryTime <= now && master.Reset == incoming.Reset && master.Reset > 0
	renewed := seen && (*incoming.ResetCount > previous || legacyTransition) && incoming.Reset > 0 && master.Reset > 0 && master.ExpiryTime > 0 && incoming.ExpiryTime > master.ExpiryTime
	if renewed {
		count, err := nextRenewalCount(master.ResetCount, max(int64(1), *incoming.ResetCount-previous))
		if err != nil {
			return master, false, false, err
		}
		master.ResetCount = &count
		master.ExpiryTime = incoming.ExpiryTime
		master.Enable = incoming.Enable
		return master, true, true, nil
	}
	limitsMatch := master.ExpiryTime == incoming.ExpiryTime && master.Total == incoming.Total
	allowed := master.ExpiryTime <= 0 || master.ExpiryTime > now
	if master.Total > 0 {
		remaining := master.Total
		for _, used := range [...]int64{master.Up, deltaUp, master.Down, deltaDown} {
			remaining -= min(remaining, max(used, 0))
		}
		allowed = allowed && remaining > 0
	}
	if !incoming.Enable && (limitsMatch || !allowed) {
		master.Enable = false
	}
	if master.ExpiryTime < 0 && incoming.ExpiryTime > 0 {
		master.ExpiryTime = incoming.ExpiryTime
	}
	return master, false, true, nil
}

func nodeSettingsExpiries(settings string) map[string]int64 {
	var obj struct {
		Clients []struct {
			Email      string `json:"email"`
			ExpiryTime *int64 `json:"expiryTime"`
		} `json:"clients"`
	}
	out := map[string]int64{}
	if json.Unmarshal([]byte(settings), &obj) != nil {
		return out
	}
	for _, client := range obj.Clients {
		if client.ExpiryTime != nil {
			out[client.Email] = *client.ExpiryTime
		}
	}
	return out
}

func liftTrackedNodeLifecycle(settings string, rows map[string]*core.ClientTraffic, tracked map[string]bool, frozen bool) (string, bool) {
	var obj map[string]any
	if json.Unmarshal([]byte(settings), &obj) != nil {
		return settings, false
	}
	clients, _ := obj["clients"].([]any)
	changed := false
	for _, raw := range clients {
		client, _ := raw.(map[string]any)
		if client == nil {
			continue
		}
		email, _ := client["email"].(string)
		row := rows[email]
		if row == nil || (!frozen && !tracked[email]) {
			continue
		}
		if enable, ok := client["enable"].(bool); !ok || enable != row.Enable {
			client["enable"] = row.Enable
			changed = true
		}
		for key, value := range map[string]int64{"expiryTime": row.ExpiryTime, "totalGB": row.Total, "reset": int64(row.Reset)} {
			if number, ok := client[key].(float64); !ok || number != float64(value) {
				client[key] = value
				changed = true
			}
		}
	}
	if !changed {
		return settings, false
	}
	raw, err := json.MarshalIndent(obj, "", "  ")
	if err != nil {
		return settings, false
	}
	return string(raw), true
}
