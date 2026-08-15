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
/*
toEngine projects one instance. serve says whether it should be running; err is
kept separate from it because they mean opposite things to the caller: a
disabled inbound must be stopped, an unreadable one must be left exactly as it
is. Collapsing both into "do not serve" is what stopped a live sidecar, and
every client on it, over settings that merely would not parse.
*/
func toEngine(inst core.Instance) (out engine.Instance, serve bool, err error) {
	out = engine.Instance{
		Id:     inst.ID,
		Tag:    inst.Tag,
		Listen: inst.Listen,
		Port:   inst.Port,
	}
	// Refuse rather than fall back to mtg's defaults: the tuning carries
	// routeThroughXray, so guessing egresses the inbound straight out instead.
	if err := out.ApplySettings(inst.Settings); err != nil {
		return out, false, err
	}
	if !inst.Enable {
		return out, false, nil
	}
	for _, u := range inst.Users {
		secret := core.CredString(u.Credentials, CredSecret)
		if !u.Enable || u.Email == "" || secret == "" {
			continue
		}
		entry := engine.SecretEntry{
			Name:   u.Email,
			Secret: secret,
			AdTag:  engine.UsableAdTag(core.CredString(u.Credentials, CredAdTag)),
		}
		if u.QuotaBytes > 0 {
			entry.QuotaBytes = u.QuotaBytes
		}
		if u.ExpiryUnixMilli > 0 {
			entry.ExpiresUnix = u.ExpiryUnixMilli / 1000
		}
		out.Secrets = append(out.Secrets, entry)
	}
	return out, len(out.Secrets) > 0, nil
}
