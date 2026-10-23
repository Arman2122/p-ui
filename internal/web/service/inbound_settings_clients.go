package service

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/util/common"
)

// Imported numeric strings are normalized once; malformed limits remain errors.
func normalizeLegacyInboundClients(settings string) (string, error) {
	var object map[string]json.RawMessage
	if json.Unmarshal([]byte(settings), &object) != nil {
		return settings, nil
	}
	var clients []map[string]json.RawMessage
	if json.Unmarshal(object["clients"], &clients) != nil {
		return settings, nil
	}
	changed := false
	for i, client := range clients {
		for _, key := range []string{"tgId", "limitIp", "totalGB", "expiryTime", "reset", "created_at", "updated_at"} {
			raw := client[key]
			if len(raw) == 0 || raw[0] != '"' {
				continue
			}
			var text string
			if err := json.Unmarshal(raw, &text); err != nil {
				return "", err
			}
			text = strings.ReplaceAll(strings.TrimSpace(text), " ", "")
			if text == "" {
				delete(client, key)
			} else {
				number, err := strconv.ParseInt(text, 10, 64)
				if err != nil {
					return "", fmt.Errorf("legacy client %d field %s: %w", i+1, key, err)
				}
				client[key] = json.RawMessage(strconv.FormatInt(number, 10))
			}
			changed = true
		}
	}
	if !changed {
		return settings, nil
	}
	object["clients"], _ = json.Marshal(clients)
	normalized, err := json.Marshal(object)
	return string(normalized), err
}

func ParseInboundSettingsClients(settings string) ([]model.Client, error) {
	trimmed := strings.TrimSpace(settings)
	if trimmed == "" || trimmed == "null" {
		return nil, common.NewError("inbound settings is empty")
	}

	var payload struct {
		Clients json.RawMessage `json:"clients"`
	}
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return nil, err
	}
	if len(payload.Clients) == 0 || string(payload.Clients) == "null" {
		return nil, nil
	}

	var clients []model.Client
	if err := json.Unmarshal(payload.Clients, &clients); err != nil {
		return nil, err
	}
	return clients, nil
}
