package runtime

import (
	"encoding/json"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
instanceOf renders one inbound row as the desired state a core reconciles towards.

Settings are healed first. Unhealed, a core and the full-config generator emit
different JSON for the same inbound, and the difference reads as a config change
that restarts Xray on top of live connections.
*/
func instanceOf(ib *model.Inbound) core.Instance {
	inst := storedInstanceOf(ib)
	inst.Settings, inst.StreamSettings = ib.HealedConfig()
	// Read from the stored settings, not the healed ones: healing turns a
	// wireguard inbound's clients into peers, which carry no email.
	inst.Users = usersOf(ib.Settings, "")
	return inst
}

/*
storedInstanceOf renders the inbound as the panel holds it, with no client
projected. It is what a core provisioning one user acts on: healing the settings
and projecting the clients each walk the whole blob — together 1.3s on a
200k-client inbound — and such a core reads neither.
*/
func storedInstanceOf(ib *model.Inbound) core.Instance {
	return core.Instance{
		ID:             ib.Id,
		Kind:           core.Kind(ib.Protocol),
		Tag:            ib.Tag,
		Listen:         ib.Listen,
		Port:           ib.Port,
		Enable:         ib.Enable,
		Settings:       ib.Settings,
		StreamSettings: ib.StreamSettings,
		Sniffing:       ib.Sniffing,
	}
}

/*
usersOf projects settings.clients onto the contract's user list. It is the view
shared across cores; a core needing more reads its own settings blob.

only names the single client to project, "" every one of them. Narrowing it
matters at scale: building a core.User and its credential map for each of 200k
clients cost 1.09s, against 0.65s to walk past them to the one named.
*/
func usersOf(settings, only string) []core.User {
	if settings == "" {
		return nil
	}
	var parsed struct {
		Clients []map[string]json.RawMessage `json:"clients"`
	}
	if err := json.Unmarshal([]byte(settings), &parsed); err != nil {
		return nil
	}
	if only != "" {
		for _, client := range parsed.Clients {
			if emailOf(client) == only {
				return []core.User{userOf(client)}
			}
		}
		return nil
	}
	users := make([]core.User, 0, len(parsed.Clients))
	for _, client := range parsed.Clients {
		user := userOf(client)
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

func emailOf(client map[string]json.RawMessage) string {
	var email string
	if raw, ok := client["email"]; ok {
		_ = json.Unmarshal(raw, &email)
	}
	return email
}

func userOf(client map[string]json.RawMessage) core.User {
	user := core.User{Credentials: make(map[string]any, len(client))}
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
			// Decoded as-is: a credential is whatever its core expects, and
			// wireguard's allowedIPs is a list, not a string.
			var value any
			if json.Unmarshal(raw, &value) == nil {
				user.Credentials[key] = value
			}
		}
	}
	return user
}
