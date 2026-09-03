/*
Package singbox drives the bundled sing-box binary for the kinds Xray does not
serve: TUIC and AnyTLS today. One process per inbound, run by the supervisor
as the p-ui-core user; per-user traffic is read from sing-box's v2ray-style
stats API and live connections from its daemon API. An edit is a config
rewrite and a reload, coalesced, which is what ApplyDelay tells the operator.
*/
package singbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/core"
)

// ID names the core in the registry.
const ID core.Kind = "singbox"

const (
	KindTUIC   core.Kind = "tuic"
	KindAnyTLS core.Kind = "anytls"
)

// Kinds is every protocol this core serves.
func Kinds() []core.Kind { return []core.Kind{KindTUIC, KindAnyTLS} }

// Settings is the inbound's own half of settings JSON; clients ride in
// Instance.Users like every other core's.
type Settings struct {
	CertFile   string `json:"certFile"`
	KeyFile    string `json:"keyFile"`
	ServerName string `json:"serverName"`
	// CongestionControl is TUIC's: bbr, cubic or new_reno.
	CongestionControl string `json:"congestionControl"`
	ZeroRTTHandshake  bool   `json:"zeroRttHandshake"`
	// PaddingScheme is AnyTLS's, one line per entry; empty keeps sing-box's default.
	PaddingScheme []string `json:"paddingScheme"`
}

func parseSettings(kind core.Kind, raw string) (Settings, error) {
	s := Settings{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			return s, fmt.Errorf("singbox: settings: %w", err)
		}
	}
	if s.CertFile == "" || s.KeyFile == "" {
		return s, errors.New("singbox: a certificate and key file are required; both TUIC and AnyTLS are TLS-only")
	}
	if kind == KindTUIC {
		switch s.CongestionControl {
		case "":
			s.CongestionControl = "bbr"
		case "bbr", "cubic", "new_reno":
		default:
			return s, fmt.Errorf("singbox: unknown congestion control %q", s.CongestionControl)
		}
	}
	return s, nil
}

// ValidateInstance refuses what the daemon would refuse at start, in words the
// operator can act on: a missing certificate is the usual one.
func (c *Core) ValidateInstance(kind core.Kind, settings string) error {
	s, err := parseSettings(kind, settings)
	if err != nil {
		return err
	}
	for _, path := range []string{s.CertFile, s.KeyFile} {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("singbox: %s: %w", path, err)
		}
	}
	return nil
}

type credential struct {
	uuid, password string
}

func credentialOf(kind core.Kind, user core.User) (credential, error) {
	cred := credential{
		uuid:     core.CredString(user.Credentials, core.CredUUID),
		password: core.CredString(user.Credentials, core.CredPassword),
	}
	if user.Email == "" {
		return cred, errors.New("singbox: a client needs an email; it is the name sing-box counts traffic under")
	}
	if strings.ContainsAny(user.Email, ">\"\\") {
		return cred, fmt.Errorf("singbox: email %q cannot name a stats counter", user.Email)
	}
	if kind == KindTUIC {
		if _, err := uuid.Parse(cred.uuid); err != nil {
			return cred, fmt.Errorf("singbox: client %s: uuid: %w", user.Email, err)
		}
	}
	if len(cred.password) < 8 {
		return cred, fmt.Errorf("singbox: client %s: password shorter than 8 characters", user.Email)
	}
	return cred, nil
}

func (c *Core) ClientCredentials(kind core.Kind) []string {
	switch kind {
	case KindTUIC:
		return []string{core.CredUUID, core.CredPassword}
	case KindAnyTLS:
		return []string{core.CredPassword}
	}
	return nil
}

func (c *Core) MintClientCredentials(kind core.Kind, _ string, have map[string]string) (map[string]string, error) {
	minted := map[string]string{}
	switch kind {
	case KindTUIC:
		if _, err := uuid.Parse(have[core.CredUUID]); err != nil {
			minted[core.CredUUID] = uuid.NewString()
		}
		if len(have[core.CredPassword]) < 8 {
			minted[core.CredPassword] = randomToken(16)
		}
	case KindAnyTLS:
		if len(have[core.CredPassword]) < 8 {
			minted[core.CredPassword] = randomToken(16)
		}
	}
	return minted, nil
}

func (c *Core) ValidateClient(kind core.Kind, _, email string, have map[string]string) error {
	if kind != KindTUIC && kind != KindAnyTLS {
		return nil
	}
	_, err := credentialOf(kind, core.User{Email: email, Enable: true, Credentials: map[string]any{
		core.CredUUID: have[core.CredUUID], core.CredPassword: have[core.CredPassword],
	}})
	return err
}

func (c *Core) ClientIdentity(core.Kind) string { return "email" }

// Transports: TUIC is QUIC and binds UDP only; AnyTLS is TLS over TCP.
func (c *Core) Transports(kind core.Kind, _, _ string) core.Transports {
	if kind == KindTUIC {
		return core.Transports{UDP: true}
	}
	return core.Transports{TCP: true}
}

func (c *Core) ProvisionsUsers(kind core.Kind) bool { return kind == KindTUIC || kind == KindAnyTLS }

/*
RenderClient is the share link the client apps import (sing-box, Clash Meta,
NekoBox, v2rayN all read tuic:// and anytls://), with the inbound's certificate
name as the SNI the client verifies.
*/
func (c *Core) RenderClient(inst core.Instance, user core.User, host string) (core.Share, error) {
	settings, err := parseSettings(inst.Kind, inst.Settings)
	if err != nil {
		return core.Share{}, err
	}
	cred, err := credentialOf(inst.Kind, user)
	if err != nil {
		return core.Share{}, err
	}
	sni := settings.ServerName
	if sni == "" {
		sni = host
	}
	q := url.Values{}
	q.Set("sni", sni)
	u := url.URL{Host: fmt.Sprintf("%s:%d", host, inst.Port)}
	u.Fragment, u.RawFragment = inst.Tag+"-"+user.Email, fragmentEscape(inst.Tag+"-"+user.Email)
	switch inst.Kind {
	case KindTUIC:
		u.Scheme = "tuic"
		u.User = url.UserPassword(cred.uuid, cred.password)
		q.Set("congestion_control", settings.CongestionControl)
		q.Set("alpn", "h3")
		q.Set("udp_relay_mode", "native")
	default:
		u.Scheme = "anytls"
		u.User = url.User(cred.password)
	}
	u.RawQuery = q.Encode()
	return core.Share{Kind: "link", URI: u.String()}, nil
}

// fragmentEscape spells the remark the way the frontend's encodeURIComponent
// does, so the two copies of every link agree byte for byte.
func fragmentEscape(s string) string {
	const keep = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.!~*'()"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(keep, s[i]) >= 0 {
			b.WriteByte(s[i])
		} else {
			fmt.Fprintf(&b, "%%%02X", s[i])
		}
	}
	return b.String()
}

// SharesFile: a tuic:// or anytls:// link is the whole share; no file.
func (c *Core) SharesFile(core.Kind) bool { return false }
