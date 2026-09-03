package ocserv

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/radius"
)

func user(email, name string) core.User {
	return core.User{Email: email, Enable: true, Credentials: map[string]any{
		core.CredUsername: name, core.CredPassword: "secret-password",
	}}
}

/*
The server's identity is minted once and never again: a profile pins the
certificate, so a re-save that rotated it would strand every client. The
camouflage secret is the same promise -- it is half of the client's URL.
*/
func TestMintInstanceFillsOnceAndKeepsForever(t *testing.T) {
	c := New(Deps{})
	first, err := c.MintInstance(Kind, `{"subnet":"10.11.0.0/24","camouflage":true}`, "")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(first), &doc); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"serverCert", "serverKey", "camouflageSecret"} {
		if v, _ := doc[key].(string); v == "" {
			t.Fatalf("%s was not minted: %s", key, first)
		}
	}
	if _, err := spkiPin(doc["serverCert"].(string)); err != nil {
		t.Fatalf("the minted certificate does not pin: %v", err)
	}
	if doc["subnet"] != "10.11.0.0/24" {
		t.Fatalf("minting touched the operator's fields: %s", first)
	}
	second, err := c.MintInstance(Kind, first, first)
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatal("a second mint rotated the identity every client pins")
	}
	// A save that arrives without the identity (an API client sending only the
	// fields it knows) gets it back from what the row held, never a new one.
	stripped, err := c.MintInstance(Kind, `{"subnet":"10.11.0.0/24","camouflage":true}`, first)
	if err != nil {
		t.Fatal(err)
	}
	var again map[string]any
	_ = json.Unmarshal([]byte(stripped), &again)
	for _, key := range []string{"serverCert", "serverKey", "camouflageSecret"} {
		if again[key] != doc[key] {
			t.Fatalf("%s was rotated by a save that omitted it", key)
		}
	}
	// Camouflage off mints no secret: an operator who never turned it on has
	// no dead field in their row.
	plain, err := c.MintInstance(Kind, `{"subnet":"10.11.0.0/24"}`, "")
	if err != nil {
		t.Fatal(err)
	}
	var off map[string]any
	_ = json.Unmarshal([]byte(plain), &off)
	if secret, _ := off["camouflageSecret"].(string); secret != "" {
		t.Fatalf("camouflage is off and a secret was minted anyway: %s", plain)
	}
	untouched, _ := c.MintInstance("vless", `{"x":1}`, "")
	if untouched != `{"x":1}` {
		t.Fatalf("another kind's settings were touched: %s", untouched)
	}
}

// A minted certificate is one nobody vouches for, so a profile has to pin it;
// the pin is the form openconnect's --servercert takes.
func TestPinIsOnlyForACertificateNobodyVouchesFor(t *testing.T) {
	certPEM, _, err := mintServerIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if !selfSigned(certPEM) {
		t.Fatal("the minted certificate does not read as self-signed")
	}
	pin, err := spkiPin(certPEM)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pin, "pin-sha256:") || len(pin) != len("pin-sha256:")+44 {
		t.Fatalf("pin %q is not a base64 SHA-256 of the public key", pin)
	}
	// The pin follows the key, not the certificate: a re-issue for the same
	// key keeps every profile working.
	other, _, _ := mintServerIdentity()
	if otherPin, _ := spkiPin(other); otherPin == pin {
		t.Fatal("two independently minted certificates pin the same")
	}
	if selfSigned("not a certificate") {
		t.Fatal("garbage read as a self-signed certificate")
	}
}

/*
A client's tunnel address is stable across reconnects, never one a live
session already holds, and never the network, the server's own or the
broadcast address.
*/
func TestAllocateIsStableAndNeverCollides(t *testing.T) {
	s, err := parseSettings(`{"subnet":"10.11.0.0/29"}`)
	if err != nil {
		t.Fatal(err)
	}
	first, err := allocate(s, "alice@x", nil)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := allocate(s, "alice@x", nil)
	if first != again {
		t.Fatalf("alice got %s then %s with nobody else connected", first, again)
	}
	held := []core.Session{{Email: "bob@x", Local: []netip.Addr{first}}}
	moved, err := allocate(s, "alice@x", held)
	if err != nil || moved == first {
		t.Fatalf("alice got %s while bob holds it (%v)", moved, err)
	}
	own := []core.Session{{Email: "alice@x", Local: []netip.Addr{first}}}
	if kept, _ := allocate(s, "alice@x", own); kept != first {
		t.Fatalf("alice reconnecting got %s instead of her own %s", kept, first)
	}
	seen := map[netip.Addr]bool{}
	var live []core.Session
	for i := 0; i < 5; i++ {
		addr, err := allocate(s, "u"+string(rune('a'+i)), live)
		if err != nil {
			t.Fatalf("client %d: %v", i, err)
		}
		if seen[addr] || addr == netip.MustParseAddr("10.11.0.0") || addr == netip.MustParseAddr("10.11.0.1") || addr == netip.MustParseAddr("10.11.0.7") {
			t.Fatalf("client %d got %s: taken or reserved", i, addr)
		}
		seen[addr] = true
		live = append(live, core.Session{Email: "u" + string(rune('a'+i)), Local: []netip.Addr{addr}})
	}
	if _, err := allocate(s, "one-too-many", live); err == nil {
		t.Fatal("a /29 admitted a sixth client")
	}
}

func TestRenderServerCarriesTheDesign(t *testing.T) {
	s, _ := parseSettings(`{"subnet":"10.11.0.0/24","dns":["1.1.1.1"],"redirectGateway":true,"camouflage":true,"camouflageSecret":"hidden"}`)
	inst := core.Instance{ID: 4, Tag: "ocs-4", Port: 443, Listen: ""}
	conf := renderServer(inst, s, pathsIn("/state/4"), "p-ui-core", 20)
	for _, want := range []string{
		"tcp-port = 443", "udp-port = 443", "run-as-user = p-ui-core", "run-as-group = p-ui-core",
		// groupconfig is what makes the Access-Accept's address and speed plan apply.
		`auth = "radius[config=` + filepath.Join("/state/4", "radiusclient.conf") + `,groupconfig=true]"`,
		`acct = "radius[config=` + filepath.Join("/state/4", "radiusclient.conf") + `]"`,
		"device = poc4-", "ipv4-network = 10.11.0.0", "ipv4-netmask = 255.255.255.0",
		"occtl-socket-file = " + filepath.Join("/state/4", "occtl.sock"), "use-occtl = true",
		"server-cert = " + filepath.Join("/state/4", "server.crt"), "stats-report-time = 20",
		"dns = 1.1.1.1", "route = default", "camouflage = true", `camouflage_secret = "hidden"`,
	} {
		if !strings.Contains(conf, want) {
			t.Fatalf("server config lacks %q:\n%s", want, conf)
		}
	}
	// Without a device separator, inbound 4's tenth session and inbound 41's
	// first would be the same interface name.
	if strings.Contains(conf, "device = poc4\n") {
		t.Fatalf("the device prefix has no separator:\n%s", conf)
	}
	off, _ := parseSettings(`{"subnet":"10.11.0.0/24"}`)
	if plain := renderServer(inst, off, pathsIn("/state/4"), "", 20); strings.Contains(plain, "camouflage") || strings.Contains(plain, "run-as-user") || strings.Contains(plain, "route = default") {
		t.Fatalf("an inbound with no camouflage, no daemon user and no redirect carries one anyway:\n%s", plain)
	}
}

/*
radcli reads three files, and the dictionary is the one that is easy to
forget: the distribution's own leaves the vendor includes commented out, so
without these lines a speed plan in an Access-Accept is silently dropped.
*/
func TestRenderRadcliNamesTheVendorDictionaries(t *testing.T) {
	p := pathsIn("/state/4")
	if _, err := renderRadcli("p-ui-ocserv-4", "127.0.0.1", "127.0.0.1:1813", p); err == nil {
		t.Fatal("an address with no port was accepted")
	}
	conf, err := renderRadcli("p-ui-ocserv-4", "127.0.0.1:1812", "127.0.0.1:1813", p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"nas-identifier p-ui-ocserv-4", "authserver 127.0.0.1:1812", "acctserver 127.0.0.1:1813", "servers " + p.servers, "dictionary " + p.dictionary} {
		if !strings.Contains(conf, want) {
			t.Fatalf("radcli config lacks %q:\n%s", want, conf)
		}
	}
	if !strings.Contains(radcliDictionary, "dictionary.roaringpenguin") {
		t.Fatalf("the dictionary does not include the speed-limit vendor:\n%s", radcliDictionary)
	}
	if got := renderServers("127.0.0.1:1812", "host-secret"); got != "127.0.0.1\thost-secret\n" {
		t.Fatalf("secret file = %q", got)
	}
}

// The profile is what a client needs and nothing it cannot use: the pin is
// there only when the certificate is the panel's own.
func TestRenderClientCarriesTheServerLoginAndPin(t *testing.T) {
	c := New(Deps{})
	minted, err := c.MintInstance(Kind, `{"subnet":"10.11.0.0/24","camouflage":true}`, "")
	if err != nil {
		t.Fatal(err)
	}
	inst := core.Instance{ID: 4, Tag: "ocs-4", Port: 443, Settings: minted}
	share, err := c.RenderClient(inst, user("alice@x", "alice"), "vpn.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if share.Kind != "file" || share.Filename != "alice.ocserv.conf" {
		t.Fatalf("share = %s %s", share.Kind, share.Filename)
	}
	var doc struct{ CamouflageSecret string }
	_ = json.Unmarshal([]byte(minted), &doc)
	for _, want := range []string{"https://vpn.example.com:443/?" + doc.CamouflageSecret, "user=alice", "password secret-password", "protocol=anyconnect", "servercert=pin-sha256:"} {
		if !strings.Contains(share.Body, want) {
			t.Fatalf("profile lacks %q:\n%s", want, share.Body)
		}
	}

	// An operator's own certificate is one the client already trusts, so the
	// profile pins nothing and the URL carries no secret.
	body, err := json.Marshal(map[string]any{"subnet": "10.11.0.0/24", "serverCert": caSignedCert(t), "serverKey": "x"})
	if err != nil {
		t.Fatal(err)
	}
	trusted := string(body)
	plain, err := c.RenderClient(core.Instance{ID: 5, Tag: "ocs-5", Port: 443, Settings: trusted}, user("alice@x", "alice"), "vpn.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain.Body, "servercert=") {
		t.Fatalf("a certificate the client trusts was pinned anyway:\n%s", plain.Body)
	}
	if !strings.Contains(plain.Body, "https://vpn.example.com:443\n") {
		t.Fatalf("the URL carries a camouflage secret that is not set:\n%s", plain.Body)
	}
}

// Accounting is billed to the email, not the login name the daemon reports.
func TestSinkBillsTheEmailBehindTheUsername(t *testing.T) {
	c := New(Deps{})
	cur := &instance{ledger: radius.NewLedger("ocs-4"), userByName: usersByName([]core.User{user("alice@x", "alice")})}
	c.instances[4] = cur
	s := &sink{c: c, id: 4}
	s.Account(t.Context(), radius.Event{Status: radius.Start, SessionID: "s1", Username: "alice"})
	s.Account(t.Context(), radius.Event{Status: radius.Interim, SessionID: "s1", Username: "alice", In: 10, Out: 20})
	s.Account(t.Context(), radius.Event{Status: radius.Interim, SessionID: "s2", Username: "nobody", In: 99, Out: 99})
	deltas, _ := c.CollectTraffic(t.Context())
	if len(deltas) != 1 || deltas[0].Email != "alice@x" || deltas[0].Up != 10 || deltas[0].Down != 20 {
		t.Fatalf("deltas = %+v", deltas)
	}
}

/*
A speed plan cannot be attached to a device that lasts one session, so the
daemon is told the numbers at login. EnforceRates records them; the sink
hands them to the next Access-Accept, and a cleared plan clears the reply.
*/
func TestRatesReachTheLoginAndAreClearedWithThePlan(t *testing.T) {
	c := New(Deps{})
	settings, err := c.MintInstance(Kind, `{"subnet":"10.11.0.0/24"}`, "")
	if err != nil {
		t.Fatal(err)
	}
	inst := core.Instance{ID: 4, Tag: "ocs-4", Port: 443, Settings: settings}
	parsed, _ := parseSettings(settings)
	c.instances[4] = &instance{
		inst: inst, settings: parsed, ledger: radius.NewLedger("ocs-4"),
		userByName: usersByName([]core.User{user("alice@x", "alice")}), rates: map[string]core.RateLimit{},
	}
	s := &sink{c: c, id: 4}
	if v := s.Authenticate(t.Context(), "alice", "secret-password", radius.Peer{}); !v.Accept || v.UpBps != 0 || v.DownBps != 0 {
		t.Fatalf("a client with no plan was given one: %+v", v)
	}
	if err := c.EnforceRates(t.Context(), inst, map[string]core.RateLimit{"alice@x": {UpBps: 1_000_000, DownBps: 8_000_000}}); err != nil {
		t.Fatal(err)
	}
	v := s.Authenticate(t.Context(), "alice", "secret-password", radius.Peer{})
	if !v.Accept || v.UpBps != 1_000_000 || v.DownBps != 8_000_000 {
		t.Fatalf("the plan did not reach the login: %+v", v)
	}
	if !v.FramedIP.IsValid() || v.InterimSeconds == 0 {
		t.Fatalf("the reply carries no address or no accounting cadence: %+v", v)
	}
	if err := c.EnforceRates(t.Context(), inst, map[string]core.RateLimit{}); err != nil {
		t.Fatal(err)
	}
	if v := s.Authenticate(t.Context(), "alice", "secret-password", radius.Peer{}); v.UpBps != 0 || v.DownBps != 0 {
		t.Fatalf("a cleared plan still throttles: %+v", v)
	}
}

/*
caSignedCert is a leaf a CA signed, which is what an operator pastes in when
their clients already trust the issuer. Minted rather than kept as a constant:
a fixture that does not parse makes the no-pin assertion pass for the wrong
reason, which is exactly what the first version of this test did.
*/
func caSignedCert(t *testing.T) string {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ca := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-ca"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(1, 0, 0),
		KeyUsage: x509.KeyUsageCertSign, BasicConstraintsValid: true, IsCA: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	leaf := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "vpn.example.com"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(1, 0, 0),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true, DNSNames: []string{"vpn.example.com"},
	}
	der, err := x509.CreateCertificate(rand.Reader, leaf, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	body := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	if selfSigned(body) {
		t.Fatal("the CA-signed fixture reads as self-signed")
	}
	if _, err := spkiPin(body); err != nil {
		t.Fatalf("the CA-signed fixture does not parse: %v", err)
	}
	return body
}
