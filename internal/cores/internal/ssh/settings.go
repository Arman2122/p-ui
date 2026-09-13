/*
Package ssh is the in-process SSH core: an SSH server inside the panel offering
direct-tcpip forwards over plain TCP, a WebSocket upgrade (bare 101 for the
HTTP-Injector family, framed RFC 6455 for a CDN in front) or WSS, and UDP
through the tunnel by badvpn's udpgw protocol (udpgw.go). No sshd, no daemon,
and every forwarded byte is counted in the process that moves it.
*/
package ssh

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/Arman2122/p-ui/internal/core"
)

// Kind is the one protocol this core serves.
const Kind core.Kind = "ssh"

// ID names the core in the registry.
const ID core.Kind = "ssh"

const (
	TransportTCP = "tcp"
	TransportWS  = "ws"
	TransportWSS = "wss"
)

// Settings is the inbound's own half of settings JSON; clients ride in
// Instance.Users like every other core's.
type Settings struct {
	Transport string `json:"transport"`
	// Path is the WebSocket path a client must ask for; empty accepts any.
	Path     string `json:"path"`
	CertFile string `json:"certFile"`
	KeyFile  string `json:"keyFile"`
	// Banner is written to a client that opens a shell, which many do only as
	// a liveness probe.
	Banner string `json:"banner"`
	/*
		RouteThroughXray sends every forward this inbound opens through Xray's
		router instead of dialling the destination directly, which is what makes
		a routing rule naming this inbound able to match. RouteXrayPort is the
		loopback port the panel allocates for that bridge: backend-owned, never
		typed, and absent until the switch is first turned on.

		Deliberately NOT refused here when the switch is on and the port is
		still zero. Validation runs before allocation on the update path, so
		refusing that pair would make the first enable impossible; the core
		fails the forward closed instead of dialling around the bridge.
	*/
	RouteThroughXray bool `json:"routeThroughXray"`
	RouteXrayPort    int  `json:"routeXrayPort"`
	// UDPGW answers badvpn's udpgw protocol on UDPGWPort inside the tunnel,
	// which is how the HTTP-Injector family carries UDP over SSH.
	UDPGW     bool `json:"udpgw"`
	UDPGWPort int  `json:"udpgwPort"`
}

func parseSettings(raw string) (Settings, error) {
	s := Settings{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			return s, fmt.Errorf("ssh: settings: %w", err)
		}
	}
	if s.Transport == "" {
		s.Transport = TransportTCP
	}
	switch s.Transport {
	case TransportTCP, TransportWS, TransportWSS:
	default:
		return s, fmt.Errorf("ssh: unknown transport %q", s.Transport)
	}
	if s.Path != "" && !strings.HasPrefix(s.Path, "/") {
		s.Path = "/" + s.Path
	}
	if s.Transport == TransportWSS && (s.CertFile == "" || s.KeyFile == "") {
		return s, errors.New("ssh: wss needs a certificate and key file")
	}
	if s.UDPGW && s.UDPGWPort == 0 {
		s.UDPGWPort = udpgwDefaultPort
	}
	if s.UDPGW && (s.UDPGWPort < 1 || s.UDPGWPort > 65535) {
		return s, fmt.Errorf("ssh: udpgw port %d is out of range", s.UDPGWPort)
	}
	return s, nil
}

// fingerprint is what a listener is built from; a change to it restarts the
// listener, a change to anything else is applied to the running one.
func fingerprint(inst core.Instance, s Settings) string {
	h := sha256.New()
	// The bridge fields are in here so flipping the switch rebuilds the
	// listener: the dialer is chosen when the server is built, and a hot apply
	// would leave live forwards on the old one.
	parts := []string{inst.Listen, fmt.Sprint(inst.Port), s.Transport, s.Path, s.CertFile, s.KeyFile}
	parts = append(parts, fmt.Sprint(s.RouteThroughXray), fmt.Sprint(s.RouteXrayPort), fmt.Sprint(s.UDPGW), fmt.Sprint(s.UDPGWPort))
	for _, part := range parts {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

var usernameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,31}$`)

// credential is one client as the server authenticates it.
type credential struct {
	email    string
	username string
	password string
	enable   bool
	expiry   int64
	limitIP  int
}

func credentialOf(u core.User) (credential, error) {
	c := credential{
		email:    u.Email,
		username: core.CredString(u.Credentials, core.CredUsername),
		password: core.CredString(u.Credentials, core.CredPassword),
		enable:   u.Enable,
		expiry:   u.ExpiryUnixMilli,
		limitIP:  u.LimitIP,
	}
	if c.email == "" {
		return c, errors.New("ssh: client has no email")
	}
	if !usernameRe.MatchString(c.username) {
		return c, fmt.Errorf("ssh: client %s: username %q must be 3-32 lowercase letters, digits, dots, dashes or underscores", c.email, c.username)
	}
	if len(c.password) < 6 {
		return c, fmt.Errorf("ssh: client %s: password must be at least 6 characters", c.email)
	}
	return c, nil
}
