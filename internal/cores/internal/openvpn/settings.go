/*
Package openvpn drives the distribution's OpenVPN as a supervised process per
inbound. Identity is CA-free: the panel mints the server certificate clients
pin by fingerprint, a tls-crypt-v2 server key wraps one client key per user so
nobody without a panel-issued profile gets a handshake, and the panel's embedded RADIUS
server authenticates and accounts through openvpn-auth-radius. The
management socket is used for one thing only: ending a session.
*/
package openvpn

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/netip"
	"strings"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
)

// ID names the core; Kind is the one protocol it serves.
const (
	ID   core.Kind = "openvpn"
	Kind core.Kind = "openvpn"
	// DevicePrefix names the tun devices: pov<id>. Letters only, for shaping.
	DevicePrefix = "pov"
)

const (
	ProtoUDP = "udp"
	ProtoTCP = "tcp"
)

// Settings is the inbound's own half of settings JSON. The three minted fields
// are filled by MintInstance on add and kept on every save: clients pin them.
type Settings struct {
	Proto  string `json:"proto"`
	Subnet string `json:"subnet"`
	// DNS is pushed to clients; empty pushes none.
	DNS             []string `json:"dns"`
	RedirectGateway bool     `json:"redirectGateway"`
	// ServerCert and ServerKey are PEM; the certificate is what a client pins.
	ServerCert string `json:"serverCert"`
	ServerKey  string `json:"serverKey"`
	// TLSCryptV2ServerKey wraps every client key; OpenVPN's own file format.
	TLSCryptV2ServerKey string `json:"tlsCryptV2ServerKey"`
}

func parseSettings(raw string) (Settings, error) {
	s := Settings{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			return s, fmt.Errorf("openvpn: settings: %w", err)
		}
	}
	if s.Proto == "" {
		s.Proto = ProtoUDP
	}
	if s.Proto != ProtoUDP && s.Proto != ProtoTCP {
		return s, fmt.Errorf("openvpn: proto must be udp or tcp, not %q", s.Proto)
	}
	if s.Subnet == "" {
		return s, errors.New("openvpn: a tunnel subnet is required")
	}
	prefix, err := netip.ParsePrefix(s.Subnet)
	if err != nil || !prefix.Addr().Is4() {
		return s, fmt.Errorf("openvpn: subnet %q is not an IPv4 prefix", s.Subnet)
	}
	if prefix.Bits() > 29 {
		return s, fmt.Errorf("openvpn: subnet %q leaves no room for clients", s.Subnet)
	}
	return s, nil
}

// pool is the subnet with the server's own first address excluded.
func (s Settings) pool() (netip.Prefix, error) {
	prefix, err := netip.ParsePrefix(s.Subnet)
	if err != nil {
		return netip.Prefix{}, err
	}
	return prefix.Masked(), nil
}

func (s Settings) netmask() string {
	prefix, _ := s.pool()
	mask := ^uint32(0) << (32 - prefix.Bits())
	return fmt.Sprintf("%d.%d.%d.%d", byte(mask>>24), byte(mask>>16), byte(mask>>8), byte(mask))
}

/*
MintInstance fills what no operator types and no file may hold: an ECDSA
server certificate valid ten years (clients pin its fingerprint, so its
expiry is a rotation event the panel will own later) and the tls-crypt-v2
server key. Whatever is present is kept: a re-save must never rotate what
every client already pins.
*/
func (c *Core) MintInstance(kind core.Kind, raw, previous string) (string, error) {
	if kind != Kind {
		return raw, nil
	}
	doc := map[string]any{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			return "", fmt.Errorf("openvpn: settings: %w", err)
		}
	}
	// What the row already held outranks a save that omits it: the fields are
	// pinned by every profile ever issued.
	if strings.TrimSpace(previous) != "" {
		old := map[string]any{}
		if json.Unmarshal([]byte(previous), &old) == nil {
			for _, key := range []string{"serverCert", "serverKey", "tlsCryptV2ServerKey"} {
				if v, _ := doc[key].(string); v == "" {
					if was, _ := old[key].(string); was != "" {
						doc[key] = was
					}
				}
			}
		}
	}
	cert, _ := doc["serverCert"].(string)
	key, _ := doc["serverKey"].(string)
	if cert == "" || key == "" {
		certPEM, keyPEM, err := mintServerIdentity()
		if err != nil {
			return "", err
		}
		doc["serverCert"], doc["serverKey"] = certPEM, keyPEM
	}
	if tc2, _ := doc["tlsCryptV2ServerKey"].(string); tc2 == "" {
		doc["tlsCryptV2ServerKey"] = mintTLSCryptV2ServerKey()
	}
	out, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func mintServerIdentity() (certPEM, keyPEM string, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return "", "", err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "p-ui-openvpn"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return "", "", err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", "", err
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})), nil
}

// mintTLSCryptV2ServerKey is what `openvpn --genkey tls-crypt-v2-server`
// writes: 128 random bytes, base64, in OpenVPN's own PEM-like block.
func mintTLSCryptV2ServerKey() string {
	raw := make([]byte, 128)
	if _, err := rand.Read(raw); err != nil {
		panic(err)
	}
	encoded := base64.StdEncoding.EncodeToString(raw)
	var b strings.Builder
	b.WriteString("-----BEGIN OpenVPN tls-crypt-v2 server key-----\n")
	for len(encoded) > 64 {
		b.WriteString(encoded[:64] + "\n")
		encoded = encoded[64:]
	}
	b.WriteString(encoded + "\n-----END OpenVPN tls-crypt-v2 server key-----\n")
	return b.String()
}

// fingerprint is the server certificate as a client pins it: SHA256, colons.
func fingerprint(certPEM string) (string, error) {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return "", errors.New("openvpn: the server certificate does not parse")
	}
	sum := sha256.Sum256(block.Bytes)
	hexed := strings.ToUpper(hex.EncodeToString(sum[:]))
	parts := make([]string, 0, 32)
	for i := 0; i < len(hexed); i += 2 {
		parts = append(parts, hexed[i:i+2])
	}
	return strings.Join(parts, ":"), nil
}

// ValidateInstance: what the daemon would refuse at start, and the pinned
// identity every existing client depends on.
func (c *Core) ValidateInstance(kind core.Kind, raw string) error {
	if kind != Kind {
		return nil
	}
	s, err := parseSettings(raw)
	if err != nil {
		return err
	}
	if s.ServerCert != "" {
		if _, err := fingerprint(s.ServerCert); err != nil {
			return err
		}
	}
	return nil
}

type credential struct {
	username, password, tlsCryptV2 string
}

// OpenVPN sanitises usernames to [A-Za-z0-9_.@-]; anything else collides after
// mangling, so the panel refuses it up front.
func credentialOf(user core.User) (credential, error) {
	cred := credential{
		username:   core.CredString(user.Credentials, core.CredUsername),
		password:   core.CredString(user.Credentials, core.CredPassword),
		tlsCryptV2: core.CredString(user.Credentials, core.CredTLSCryptV2),
	}
	if len(cred.username) < 3 || len(cred.username) > 64 {
		return cred, fmt.Errorf("openvpn: client %s: username must be 3-64 characters", user.Email)
	}
	for _, r := range cred.username {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.', r == '@', r == '-':
		default:
			return cred, fmt.Errorf("openvpn: client %s: username %q may hold only letters, digits, _ . @ -", user.Email, cred.username)
		}
	}
	if len(cred.password) < 8 {
		return cred, fmt.Errorf("openvpn: client %s: password shorter than 8 characters", user.Email)
	}
	return cred, nil
}

func (c *Core) ClientCredentials(kind core.Kind) []string {
	if kind != Kind {
		return nil
	}
	return []string{core.CredUsername, core.CredPassword, core.CredTLSCryptV2}
}

func (c *Core) ClientIdentity(core.Kind) string { return "email" }

// Transports: the one protocol the inbound's proto names, on its port.
func (c *Core) Transports(kind core.Kind, settings, _ string) core.Transports {
	if kind != Kind {
		return core.Transports{}
	}
	if s, err := parseSettings(settings); err == nil && s.Proto == ProtoTCP {
		return core.Transports{TCP: true}
	}
	return core.Transports{UDP: true}
}

func (c *Core) ProvisionsUsers(kind core.Kind) bool { return kind == Kind }

func (c *Core) DeviceNamespace() core.DeviceNamespace {
	return core.DeviceNamespace{Prefix: DevicePrefix}
}

func deviceName(id int) string { return fmt.Sprintf("%s%d", DevicePrefix, id) }
