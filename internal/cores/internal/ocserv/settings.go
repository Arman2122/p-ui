/*
Package ocserv drives the distribution's OpenConnect server as a supervised
process per inbound. Clients speak the AnyConnect protocol over TLS and DTLS
on one port; they present no certificate and log in with a username and
password, which the panel's embedded RADIUS server answers. That same reply
carries the client's tunnel address and speed plan, so a client edit reaches
the daemon without touching a file. occtl is used for one thing only: ending
a session the panel has already refused.
*/
package ocserv

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/netip"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
)

// ID names the core; Kind is the one protocol it serves.
const (
	ID   core.Kind = "ocserv"
	Kind core.Kind = "ocserv"
	/*
		DevicePrefix names the tun devices. ocserv makes one per SESSION and names
		it <device><n>, so the inbound's own part must end in a separator: without
		one, inbound 7's eighth session and inbound 78's first are both poc78.
	*/
	DevicePrefix = "poc"
)

/*
Settings is the inbound's own half of settings JSON. The server certificate
and key are minted on the first save and kept on every later one, because a
client that pinned the certificate stops connecting the moment it changes;
an operator who has a real certificate for the host pastes it into the same
two fields, which is what the native AnyConnect client needs.
*/
type Settings struct {
	// Subnet is the tunnel pool; the panel picks each client's address from it.
	Subnet string `json:"subnet"`
	// DNS is pushed to clients; empty pushes none.
	DNS []string `json:"dns"`
	// RedirectGateway sends all of a client's traffic through the tunnel.
	RedirectGateway bool `json:"redirectGateway"`
	// ServerCert and ServerKey are PEM. Minted self-signed when empty.
	ServerCert string `json:"serverCert"`
	ServerKey  string `json:"serverKey"`
	/*
		Camouflage makes the daemon answer everything that does not carry the
		secret as an ordinary web server would, which is what keeps an
		AnyConnect inbound reachable where the protocol itself is blocked. The
		secret rides the client's URL after a '?'.
	*/
	Camouflage       bool   `json:"camouflage"`
	CamouflageSecret string `json:"camouflageSecret"`
}

func parseSettings(raw string) (Settings, error) {
	s := Settings{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &s); err != nil {
			return s, fmt.Errorf("ocserv: settings: %w", err)
		}
	}
	if s.Subnet == "" {
		return s, errors.New("ocserv: a tunnel subnet is required")
	}
	prefix, err := netip.ParsePrefix(s.Subnet)
	if err != nil || !prefix.Addr().Is4() {
		return s, fmt.Errorf("ocserv: subnet %q is not an IPv4 prefix", s.Subnet)
	}
	if prefix.Bits() > 29 {
		return s, fmt.Errorf("ocserv: subnet %q leaves no room for clients", s.Subnet)
	}
	if s.Camouflage && strings.TrimSpace(s.CamouflageSecret) == "" {
		return s, errors.New("ocserv: camouflage is on with no secret")
	}
	// The secret rides a URL query and ocserv's config is quoted, so anything
	// that would break either is refused rather than escaped.
	if strings.ContainsAny(s.CamouflageSecret, "\"' &?#/\\\r\n") {
		return s, fmt.Errorf("ocserv: camouflage secret %q holds a character a URL cannot carry", s.CamouflageSecret)
	}
	return s, nil
}

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
MintInstance fills what no operator types: an ECDSA server certificate valid
ten years, and the camouflage secret when camouflage is on. Whatever the row
already held wins over a save that omits it, so a re-save never rotates the
certificate every client pins.
*/
func (c *Core) MintInstance(kind core.Kind, raw, previous string) (string, error) {
	if kind != Kind {
		return raw, nil
	}
	doc := map[string]any{}
	if strings.TrimSpace(raw) != "" {
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			return "", fmt.Errorf("ocserv: settings: %w", err)
		}
	}
	if strings.TrimSpace(previous) != "" {
		old := map[string]any{}
		if json.Unmarshal([]byte(previous), &old) == nil {
			for _, key := range []string{"serverCert", "serverKey", "camouflageSecret"} {
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
	if on, _ := doc["camouflage"].(bool); on {
		if secret, _ := doc["camouflageSecret"].(string); secret == "" {
			doc["camouflageSecret"] = randomToken(24)
		}
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
		Subject:               pkix.Name{CommonName: "p-ui-ocserv"},
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

// spkiPin is the server certificate as an OpenConnect client pins it: the
// base64 SHA-256 of the SubjectPublicKeyInfo, which survives a re-issue of
// the same key. A certificate signed by a public CA needs no pin at all.
func spkiPin(certPEM string) (string, error) {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return "", errors.New("ocserv: the server certificate does not parse")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("ocserv: the server certificate does not parse: %w", err)
	}
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return "pin-sha256:" + base64.StdEncoding.EncodeToString(sum[:]), nil
}

/*
selfSigned reports a certificate nobody else vouches for, which is the one a
client has to pin; an operator's own certificate is trusted as it is. Asked
as issuer-equals-subject plus a signature it made itself, rather than
CheckSignatureFrom, which refuses a signer that is not a CA -- and the
panel's certificate deliberately is not one.
*/
func selfSigned(certPEM string) bool {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return false
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil || !bytes.Equal(cert.RawIssuer, cert.RawSubject) {
		return false
	}
	return cert.CheckSignature(cert.SignatureAlgorithm, cert.RawTBSCertificate, cert.Signature) == nil
}

// ValidateInstance: what the daemon would refuse at start, the pinned identity
// every existing client depends on, and the one option an older daemon accepts
// without honouring.
func (c *Core) ValidateInstance(kind core.Kind, raw string) error {
	if kind != Kind {
		return nil
	}
	s, err := parseSettings(raw)
	if err != nil {
		return err
	}
	if s.ServerCert != "" {
		if _, err := spkiPin(s.ServerCert); err != nil {
			return err
		}
	}
	if s.Camouflage {
		if v := c.daemonVersion(); v != "" && !supportsCamouflage(v) {
			return fmt.Errorf("ocserv: camouflage needs ocserv 1.2 or newer; this host has %s", v)
		}
	}
	return nil
}

/*
An ocserv older than 1.2 does not refuse `camouflage`, it prints "skipping
unknown option" and serves the inbound unhidden -- so an operator who turned
it on would believe they were camouflaged and not be. That is the one thing
worth reading the daemon's version for.
*/
func supportsCamouflage(version string) bool {
	major, minor, ok := strings.Cut(version, ".")
	if !ok {
		return false
	}
	minor, _, _ = strings.Cut(minor, ".")
	m, err := strconv.Atoi(strings.TrimSpace(major))
	if err != nil {
		return false
	}
	n, err := strconv.Atoi(strings.TrimSpace(minor))
	if err != nil {
		return false
	}
	return m > 1 || (m == 1 && n >= 2)
}

// daemonVersion is the number in "OpenConnect VPN Server 1.2.4", or empty when
// the daemon is absent or answers something unexpected: an unknown version
// refuses nothing, because Preflight already reports a missing daemon.
func (c *Core) daemonVersion() string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, c.binaryPath(), "--version").CombinedOutput()
	if err != nil && len(out) == 0 {
		return ""
	}
	for _, field := range strings.Fields(string(out)) {
		if field != "" && field[0] >= '0' && field[0] <= '9' && strings.Contains(field, ".") {
			return field
		}
	}
	return ""
}

type credential struct{ username, password string }

/*
ocserv's username reaches a RADIUS User-Name, a ccd-style per-user file name
and the occtl command line, so it is kept to what all three carry without
quoting. The password is the one a client types.
*/
func credentialOf(user core.User) (credential, error) {
	cred := credential{
		username: core.CredString(user.Credentials, core.CredUsername),
		password: core.CredString(user.Credentials, core.CredPassword),
	}
	if len(cred.username) < 3 || len(cred.username) > 64 {
		return cred, fmt.Errorf("ocserv: client %s: username must be 3-64 characters", user.Email)
	}
	for _, r := range cred.username {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.', r == '@', r == '-':
		default:
			return cred, fmt.Errorf("ocserv: client %s: username %q may hold only letters, digits, _ . @ -", user.Email, cred.username)
		}
	}
	if len(cred.password) < 8 {
		return cred, fmt.Errorf("ocserv: client %s: password shorter than 8 characters", user.Email)
	}
	return cred, nil
}

func (c *Core) ClientCredentials(kind core.Kind) []string {
	if kind != Kind {
		return nil
	}
	return []string{core.CredUsername, core.CredPassword}
}

func (c *Core) ClientIdentity(core.Kind) string { return "email" }

// Transports: one port carries both, TLS for the session and DTLS for the
// data channel, so the port conflict check must claim both.
func (c *Core) Transports(kind core.Kind, _, _ string) core.Transports {
	if kind != Kind {
		return core.Transports{}
	}
	return core.Transports{TCP: true, UDP: true}
}

func (c *Core) ProvisionsUsers(kind core.Kind) bool { return kind == Kind }

// DeviceNamespace: one device per session, named by the daemon from the
// inbound's prefix, so the family is what the shaping plane owns.
func (c *Core) DeviceNamespace() core.DeviceNamespace {
	return core.DeviceNamespace{Prefix: DevicePrefix, PerSession: true}
}

// devicePrefixFor is what the daemon is given; it appends a session number,
// so the trailing separator is what keeps two inbounds' devices apart.
func devicePrefixFor(id int) string { return fmt.Sprintf("%s%d-", DevicePrefix, id) }
