package runtime

import (
	"encoding/json"
	"strconv"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
)

// instanceOf renders one inbound row as the desired state a core reconciles
// towards. Settings are healed first: unhealed, a core and the full-config
// generator emit different JSON for the same inbound, and the difference reads
// as a config change that restarts Xray on top of live connections.
func instanceOf(ib *model.Inbound) core.Instance {
	settings, streamSettings := ib.HealedConfig()
	return core.Instance{
		ID:             ib.Id,
		Kind:           core.Kind(ib.Protocol),
		Tag:            ib.Tag,
		Listen:         ib.Listen,
		Port:           ib.Port,
		Enable:         ib.Enable,
		Settings:       settings,
		StreamSettings: streamSettings,
		Sniffing:       ib.Sniffing,
		// Read from the stored settings, not the healed ones: healing turns a
		// wireguard inbound's clients into peers, which carry no email.
		Users: usersOf(ib.Settings),
	}
}

// usersOf projects settings.clients onto the contract's user list. It is the
// view shared across cores; a core needing more reads its own settings blob.
func usersOf(settings string) []core.User {
	if settings == "" {
		return nil
	}
	var parsed struct {
		Clients []map[string]json.RawMessage `json:"clients"`
	}
	if err := json.Unmarshal([]byte(settings), &parsed); err != nil {
		return nil
	}
	users := make([]core.User, 0, len(parsed.Clients))
	for _, client := range parsed.Clients {
		user := core.User{Credentials: make(map[string]string, len(client))}
		for key, raw := range client {
			switch key {
			case "email":
				_ = json.Unmarshal(raw, &user.Email)
			case "enable":
				_ = json.Unmarshal(raw, &user.Enable)
			case "totalGB":
				_ = json.Unmarshal(raw, &user.QuotaBytes)
			case "expiryTime":
				_ = json.Unmarshal(raw, &user.ExpiryUnixMilli)
			default:
				if value, ok := scalar(raw); ok {
					user.Credentials[key] = value
				}
			}
		}
		// A client with no email cannot be billed, revoked or looked up, so the
		// panel has no way to act on it and neither has a core.
		if user.Email == "" {
			continue
		}
		users = append(users, user)
	}
	if len(users) == 0 {
		return nil
	}
	return users
}

// scalar renders a JSON scalar as the string a credential map holds. Objects and
// arrays are skipped rather than encoded: nothing reads them from here, and they
// are rendered from the settings blob, which stays authoritative.
func scalar(raw json.RawMessage) (string, bool) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	switch typed := value.(type) {
	case string:
		return typed, true
	case bool:
		return strconv.FormatBool(typed), true
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), true
	}
	return "", false
}
