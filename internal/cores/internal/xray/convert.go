package xray

import (
	"fmt"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/util/json_util"
)

/*
toInbound renders one instance as the inbound Xray consumes. ok is false when the
instance is disabled, so callers drop it from the generated config rather than
emitting a listener nobody should be able to reach.

The three sections are passed through byte-for-byte. Rebuilding settings.clients
from Instance.Users would be the obvious move and is wrong twice: it loses every
credential the panel does not keep as a scalar (wireguard allowedIPs, vless
testseed), and re-marshalling sorts the keys and compacts what a healer
indented, so an unchanged inbound renders differently here than in the
full-config generator — and InboundConfig.Equals reads that as a change and
restarts Xray under live connections. For this core the blob is the authority.
*/
func toInbound(inst core.Instance) (core.InboundConfig, bool) {
	listen := inst.Listen
	if listen == "" {
		listen = "0.0.0.0"
	}
	out := core.InboundConfig{
		Listen:         json_util.RawMessage(fmt.Sprintf("%q", listen)),
		Port:           inst.Port,
		Protocol:       string(inst.Kind),
		Settings:       json_util.RawMessage(inst.Settings),
		StreamSettings: json_util.RawMessage(inst.StreamSettings),
		Tag:            inst.Tag,
		Sniffing:       json_util.RawMessage(inst.Sniffing),
	}
	return out, inst.Enable
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
