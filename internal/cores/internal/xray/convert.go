package xray

import (
	"encoding/json"
	"fmt"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/util/json_util"
)

// settingsEnvelope is the Xray-specific half of an inbound: the sections that
// are not already fields on core.Instance. Users are NOT read from here.
type settingsEnvelope struct {
	Settings       json_util.RawMessage `json:"settings"`
	StreamSettings json_util.RawMessage `json:"streamSettings"`
	Sniffing       json_util.RawMessage `json:"sniffing"`
}

// toInbound renders one instance as the inbound Xray consumes. ok is false when
// the instance is disabled, so callers drop it from the generated config rather
// than emitting a listener nobody should be able to reach.
func toInbound(inst core.Instance) (core.InboundConfig, bool) {
	var env settingsEnvelope
	if len(inst.Settings) > 0 {
		if err := json.Unmarshal([]byte(inst.Settings), &env); err != nil {
			return core.InboundConfig{}, false
		}
	}
	listen := inst.Listen
	if listen == "" {
		listen = "0.0.0.0"
	}
	out := core.InboundConfig{
		Listen:         json_util.RawMessage(fmt.Sprintf("%q", listen)),
		Port:           inst.Port,
		Protocol:       string(inst.Kind),
		Settings:       withClients(env.Settings, inst.Users),
		StreamSettings: env.StreamSettings,
		Tag:            inst.Tag,
		Sniffing:       env.Sniffing,
	}
	return out, inst.Enable
}

// withClients puts the contract's user list into settings.clients, which is
// where Xray looks. The instance's Users are authoritative: a client the
// contract has dropped must not survive inside the stored settings blob.
func withClients(settings json_util.RawMessage, users []core.User) json_util.RawMessage {
	fields := map[string]any{}
	if len(settings) > 0 {
		if err := json.Unmarshal(settings, &fields); err != nil {
			return settings
		}
	}
	clients := make([]any, 0, len(users))
	for _, u := range users {
		if !u.Enable || u.Email == "" {
			continue
		}
		clients = append(clients, clientOf(u))
	}
	if len(clients) == 0 {
		delete(fields, "clients")
	} else {
		fields["clients"] = clients
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		return settings
	}
	return encoded
}

// clientOf renders one user the way its protocol expects. The credential names
// are the core's own; nothing above it knows whether a client carries an id, a
// password or a public key.
func clientOf(u core.User) map[string]any {
	client := map[string]any{"email": u.Email}
	for key, value := range u.Credentials {
		client[key] = value
	}
	return client
}
