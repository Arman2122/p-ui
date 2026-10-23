package ocserv

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"errors"
	"fmt"
	"net/netip"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/radius"
)

var ErrNodeConfiguration = errors.New("invalid headless ocserv configuration")

func ParseNodeSettings(raw string) (Settings, error) { return parseSettings(raw) }

type NodeConfiguration struct {
	ID               uint32       `json:"id"`
	InboundTag       string       `json:"inboundTag,omitempty"`
	Port             uint16       `json:"port"`
	Listen           netip.Addr   `json:"listen"`
	MTU              uint16       `json:"mtu"`
	DNS              []netip.Addr `json:"dns,omitempty"`
	RedirectGateway  bool         `json:"redirectGateway,omitempty"`
	ServerCert       string       `json:"serverCert"`
	ServerKey        string       `json:"serverKey"`
	CamouflageSecret string       `json:"camouflageSecret,omitempty"`
	Clients          []NodeClient `json:"clients"`
}

type NodeClient struct {
	ClientID    string       `json:"clientId"`
	Username    string       `json:"username"`
	Password    string       `json:"password"`
	Subnet      netip.Prefix `json:"subnet"`
	MaxSessions uint16       `json:"maxSessions"`
}

func (c NodeConfiguration) Prefix() string { return fmt.Sprintf("phvc%dx", c.ID) }

func (c NodeConfiguration) ValidateServer() error {
	if c.ID == 0 || c.ID > 9999 || len(c.InboundTag) > 128 || strings.ContainsRune(c.InboundTag, 0) || c.Port < 1024 || !c.Listen.Is4() || c.Listen.IsMulticast() || c.MTU < 1280 || c.MTU > 1420 || len(c.DNS) > 8 || len(c.ServerCert)+len(c.ServerKey) > 32768 {
		return ErrNodeConfiguration
	}
	if _, err := tls.X509KeyPair([]byte(c.ServerCert), []byte(c.ServerKey)); err != nil {
		return ErrNodeConfiguration
	}
	if len(c.CamouflageSecret) > 128 || strings.ContainsAny(c.CamouflageSecret, "\"' &?#/\\\r\n\x00") {
		return ErrNodeConfiguration
	}
	for _, ip := range c.DNS {
		if !ip.IsGlobalUnicast() || ip.Zone() != "" || ip.Is4In6() {
			return ErrNodeConfiguration
		}
	}
	return nil
}

func (c NodeConfiguration) Validate() error {
	if err := c.ValidateServer(); err != nil {
		return err
	}
	if len(c.Clients) == 0 || len(c.Clients) > 60000 {
		return ErrNodeConfiguration
	}
	names, ids := map[string]bool{}, map[string]bool{}
	for _, client := range c.Clients {
		if _, err := credentialOf(core.User{Credentials: map[string]any{core.CredUsername: client.Username, core.CredPassword: client.Password}}); err != nil {
			return ErrNodeConfiguration
		}
		if names[client.Username] || ids[client.ClientID] || client.ClientID == "" || len(client.Password) > 512 || strings.ContainsAny(client.Password, "\r\n\x00") || !client.Subnet.Addr().Is4() || !client.Subnet.Addr().IsPrivate() || client.Subnet != client.Subnet.Masked() || client.Subnet.Bits() < 24 || client.Subnet.Bits() > 29 || client.MaxSessions == 0 || client.MaxSessions > 32 || int(client.MaxSessions) > (1<<(32-client.Subnet.Bits()))-3 {
			return ErrNodeConfiguration
		}
		names[client.Username], ids[client.ClientID] = true, true
	}
	return nil
}

func (c NodeClient) Permits(ip netip.Addr) bool {
	return ip.Is4() && c.Subnet.Contains(ip) && ip != c.Subnet.Addr() && ip != c.Subnet.Addr().Next() && c.Subnet.Contains(ip.Next())
}

func (c NodeConfiguration) Files(dir, hook, user, group string, server *radius.Server) (map[string]string, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	for _, value := range []string{dir, hook, user, group} {
		if value == "" || strings.ContainsAny(value, " \t\r\n\"\\\x00") {
			return nil, ErrNodeConfiguration
		}
	}
	p := pathsIn(dir)
	nas := radius.NASIdentifier("node-ocserv", int(c.ID))
	radcli, err := renderRadcli(nas, server.AuthAddr(), server.AcctAddr(), p)
	if err != nil {
		return nil, err
	}
	var out strings.Builder
	fmt.Fprintf(&out, "auth = \"radius[config=%s]\"\nconfig-per-user = %s\n", p.radcli, filepath.Join(dir, "users"))
	fmt.Fprintf(&out, "listen-host = %s\ntcp-port = %d\nudp-port = %d\nrun-as-user = %s\nrun-as-group = %s\n", c.Listen, c.Port, c.Port, user, group)
	fmt.Fprintf(&out, "socket-file = %s\nocctl-socket-file = %s\nuse-occtl = true\npid-file = %s\n", p.sock, p.ctl, p.pid)
	fmt.Fprintf(&out, "server-cert = %s\nserver-key = %s\ndevice = %s\n", p.cert, p.key, c.Prefix())
	fmt.Fprintf(&out, "ipv4-network = %s\n", c.Clients[0].Subnet)
	fmt.Fprintf(&out, "connect-script = %s\ndisconnect-script = %s\n", hook, hook)
	out.WriteString("isolate-workers = true\nuse-utmp = false\nmax-clients = 60000\nmax-same-clients = 32\npredictable-ips = false\nping-leases = false\nauth-timeout = 60\ncookie-timeout = 60\nkeepalive = 60\ndpd = 15\nmobile-dpd = 30\nswitch-to-tcp-timeout = 5\ncisco-client-compat = true\ndtls-legacy = true\n")
	out.WriteString("tls-priorities = \"NORMAL:%SERVER_PRECEDENCE:%COMPAT:-RSA:-VERS-SSL3.0:-VERS-TLS1.0:-VERS-TLS1.1:-ARCFOUR-128\"\n")
	fmt.Fprintf(&out, "mtu = %d\n", c.MTU)
	if c.CamouflageSecret != "" {
		fmt.Fprintf(&out, "camouflage = true\ncamouflage_secret = \"%s\"\ncamouflage_realm = \"Restricted Content\"\n", c.CamouflageSecret)
	}
	for _, ip := range c.DNS {
		fmt.Fprintf(&out, "dns = %s\n", ip)
	}
	if c.RedirectGateway {
		out.WriteString("route = default\ntunnel-all-dns = true\n")
	}
	files := map[string]string{p.conf: out.String(), p.cert: c.ServerCert, p.key: c.ServerKey, p.radcli: radcli, p.servers: renderServers(server.AuthAddr(), server.Secret()), p.dictionary: radcliDictionary}
	for _, client := range c.Clients {
		body := fmt.Sprintf("ipv4-network = %s\nmax-same-clients = %d\n", client.Subnet, client.MaxSessions)
		if !c.RedirectGateway {
			body += fmt.Sprintf("route = %s\n", client.Subnet)
		}
		files[filepath.Join(dir, "users", client.Username)] = body
	}
	return files, nil
}

type NodeAuthenticator struct {
	mu     sync.RWMutex
	users  map[string]*nodeAuthentication
	grants map[string]governor.Grant
	clock  limits.Clock
}

type nodeAuthentication struct {
	client NodeClient
	proof  *admission.BoundProof
	scoped bool
}

func (a *NodeAuthenticator) SetClock(clock limits.Clock) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.clock = clock
}

func NewNodeAuthenticator(config NodeConfiguration) *NodeAuthenticator {
	a := &NodeAuthenticator{users: map[string]*nodeAuthentication{}}
	for _, client := range config.Clients {
		a.users[client.Username] = &nodeAuthentication{client: client}
	}
	return a
}

func (a *NodeAuthenticator) BindAdmission(binding *admission.Binding, tag string) error {
	if binding == nil || tag == "" {
		return admission.ErrUnavailable
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	next := make(map[string]*nodeAuthentication, len(a.users))
	for name, old := range a.users {
		proof, found := binding.Capture(old.client.ClientID, tag)
		if !found {
			return admission.ErrUnavailable
		}
		next[name] = old
		if !old.scoped || old.proof != proof {
			next[name] = &nodeAuthentication{client: old.client, proof: proof, scoped: true}
		}
	}
	a.users = next
	return nil
}

func (c *nodeAuthentication) current() bool {
	return c != nil && (!c.scoped || c.proof.Current())
}

func (a *NodeAuthenticator) AuthenticationCurrent(username string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.users[username].current()
}

func (a *NodeAuthenticator) Grants(grants []governor.Grant) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.grants = make(map[string]governor.Grant, len(grants))
	for _, grant := range grants {
		a.grants[grant.ClientID] = grant
	}
}

func (a *NodeAuthenticator) Authenticate(ctx context.Context, username, password string, _ radius.Peer) radius.Verdict {
	a.mu.RLock()
	credential, clock := a.users[username], a.clock
	a.mu.RUnlock()
	if credential == nil {
		return radius.Verdict{Reason: "client authentication failed"}
	}
	provided, expected := sha256.Sum256([]byte(password)), sha256.Sum256([]byte(credential.client.Password))
	if subtle.ConstantTimeCompare(provided[:], expected[:]) != 1 {
		return radius.Verdict{Reason: "client authentication failed"}
	}
	denied := radius.Verdict{Authenticated: true, Reason: "client authorization unavailable"}
	now := time.Now()
	if clock != nil {
		var err error
		now, err = clock.Now()
		if err != nil {
			return denied
		}
	}
	a.mu.RLock()
	if a.users[username] != credential || !credential.current() || ctx.Err() != nil {
		a.mu.RUnlock()
		return denied
	}
	grant, authorized := a.grants[credential.client.ClientID]
	eligible := authorized && grant.Policy.Enabled && (grant.Policy.ExpiresUnixMilli == 0 || grant.Policy.ExpiresUnixMilli > now.UnixMilli()) &&
		(!credential.scoped || grant.Policy.Upload.Mode != limits.Blocked && grant.Policy.Download.Mode != limits.Blocked && (grant.Policy.QuotaUnlimited || grant.Policy.CreditCeiling != 0))
	a.mu.RUnlock()
	if !eligible {
		if credential.scoped {
			_ = credential.proof.Request(ctx)
		}
		return denied
	}
	return radius.Verdict{Accept: true, Authenticated: true}
}

func (*NodeAuthenticator) Account(context.Context, radius.Event) {}
func NodeReady(ctx context.Context, dir string) error {
	return occtlStatus(ctx, "/usr/bin/occtl", pathsIn(dir).ctl)
}
