package mtproto

import (
	"github.com/Arman2122/p-ui/internal/core"
	engine "github.com/Arman2122/p-ui/internal/mtproto"
)

// Credential keys this core expects on a client. They are the map keys in
// core.User.Credentials and, once client_credentials lands, its rows.
const (
	CredSecret = "secret"
	CredAdTag  = "adTag"
)

// toEngine converts desired state into the engine's shape. ok is false when the
// instance cannot be served — disabled, or with no client holding a secret.
func toEngine(inst core.Instance) (engine.Instance, bool) {
	out := engine.Instance{
		Id:     inst.ID,
		Tag:    inst.Tag,
		Listen: inst.Listen,
		Port:   inst.Port,
	}
	// Refuse rather than fall back to mtg's defaults: the tuning carries
	// routeThroughXray, so guessing egresses the inbound straight out instead.
	if err := out.ApplySettings(inst.Settings); err != nil {
		return out, false
	}
	if !inst.Enable {
		return out, false
	}
	for _, u := range inst.Users {
		secret := u.Credentials[CredSecret]
		if !u.Enable || u.Email == "" || secret == "" {
			continue
		}
		entry := engine.SecretEntry{
			Name:   u.Email,
			Secret: secret,
			AdTag:  engine.UsableAdTag(u.Credentials[CredAdTag]),
		}
		if u.QuotaBytes > 0 {
			entry.QuotaBytes = u.QuotaBytes
		}
		if u.ExpiryUnixMilli > 0 {
			entry.ExpiresUnix = u.ExpiryUnixMilli / 1000
		}
		out.Secrets = append(out.Secrets, entry)
	}
	return out, len(out.Secrets) > 0
}
