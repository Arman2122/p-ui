package wireguard

import (
	"github.com/Arman2122/p-ui/internal/core"
	engine "github.com/Arman2122/p-ui/internal/wireguard"
)

// credKeepAlive is the client's persistent-keepalive interval, read by its bare
// name because it is outside the credential vocabulary a core may declare.
const credKeepAlive = "keepAlive"

// toEngine converts desired state into the engine's shape. serve is false only for
// an inbound that must serve nobody; settings it cannot read are an error, not that.
func toEngine(inst core.Instance) (engine.Instance, bool, error) {
	// Port is the inbound's own. Nothing reads a listen port out of the settings
	// blob, so editing the port in the UI is a change the device actually sees.
	out := engine.Instance{ID: inst.ID, Tag: inst.Tag, Port: inst.Port}
	if err := out.ApplySettings(inst.Settings); err != nil {
		return out, false, err
	}
	for _, u := range inst.Users {
		peer, ok := toPeer(u)
		if !ok || !u.Enable {
			continue
		}
		out.Peers = append(out.Peers, peer)
	}
	return out, inst.Enable && out.PrivateKey != "", nil
}

// toPeer renders one client as the kernel holds it. ok is false without a public
// key: the kernel authenticates by key alone, so there would be nothing to add.
func toPeer(u core.User) (engine.Peer, bool) {
	key := core.CredString(u.Credentials, core.CredPublicKey)
	if u.Email == "" || key == "" {
		return engine.Peer{}, false
	}
	return engine.Peer{
		Email:        u.Email,
		PublicKey:    key,
		PreSharedKey: core.CredString(u.Credentials, core.CredPreSharedKey),
		AllowedIPs:   credStrings(u.Credentials, core.CredAllowedIPs),
		KeepAlive:    credInt(u.Credentials, credKeepAlive),
	}, true
}

// credStrings reads a credential the panel stores as a list. allowedIPs is one,
// and a settings blob decoded into any yields []any rather than []string.
func credStrings(credentials map[string]any, key string) []string {
	switch value := credentials[key].(type) {
	case []string:
		return value
	case []any:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// credInt reads a numeric credential. JSON decodes every number into float64, so
// an int arrives only from a caller that built the map in Go.
func credInt(credentials map[string]any, key string) int {
	switch value := credentials[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	}
	return 0
}
