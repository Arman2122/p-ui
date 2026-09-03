package openvpn

import (
	"encoding/json"
	"net/netip"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/radius"
)

func user(email, name string) core.User {
	return core.User{Email: email, Enable: true, Credentials: map[string]any{
		core.CredUsername: name, core.CredPassword: "secret-password", core.CredTLSCryptV2: "-----BEGIN OpenVPN tls-crypt-v2 client key-----\nAAAA\n-----END OpenVPN tls-crypt-v2 client key-----\n",
	}}
}

/*
The server's identity is minted once and never again: clients pin the
certificate by fingerprint and every client key is wrapped by the server key,
so a re-save that rotated either would strand every profile ever issued.
*/
func TestMintInstanceFillsOnceAndKeepsForever(t *testing.T) {
	c := New(Deps{})
	first, err := c.MintInstance(Kind, `{"proto":"udp","subnet":"10.9.0.0/24"}`, "")
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(first), &doc); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"serverCert", "serverKey", "tlsCryptV2ServerKey"} {
		if v, _ := doc[key].(string); v == "" {
			t.Fatalf("%s was not minted: %s", key, first)
		}
	}
	if !strings.HasPrefix(doc["tlsCryptV2ServerKey"].(string), "-----BEGIN OpenVPN tls-crypt-v2 server key-----\n") {
		t.Fatalf("server key is not in OpenVPN's block format: %q", doc["tlsCryptV2ServerKey"])
	}
	if _, err := fingerprint(doc["serverCert"].(string)); err != nil {
		t.Fatalf("the minted certificate does not fingerprint: %v", err)
	}
	if doc["proto"] != "udp" || doc["subnet"] != "10.9.0.0/24" {
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
	stripped, err := c.MintInstance(Kind, `{"proto":"udp","subnet":"10.9.0.0/24"}`, first)
	if err != nil {
		t.Fatal(err)
	}
	var again map[string]any
	_ = json.Unmarshal([]byte(stripped), &again)
	for _, key := range []string{"serverCert", "serverKey", "tlsCryptV2ServerKey"} {
		if again[key] != doc[key] {
			t.Fatalf("%s was rotated by a save that omitted it", key)
		}
	}
	untouched, _ := c.MintInstance("vless", `{"x":1}`, "")
	if untouched != `{"x":1}` {
		t.Fatalf("another kind's settings were touched: %s", untouched)
	}
}

// The fingerprint is spelled the way OpenVPN's peer-fingerprint wants it, the
// same form `openssl x509 -fingerprint -sha256` prints.
func TestFingerprintMatchesOpenSSL(t *testing.T) {
	certPEM, _, err := mintServerIdentity()
	if err != nil {
		t.Fatal(err)
	}
	fp, err := fingerprint(certPEM)
	if err != nil {
		t.Fatal(err)
	}
	if len(fp) != 95 || strings.Count(fp, ":") != 31 || strings.ToUpper(fp) != fp {
		t.Fatalf("fingerprint %q is not 32 upper-case hex pairs joined by colons", fp)
	}
	if _, err := exec.LookPath("openssl"); err != nil {
		return
	}
	cmd := exec.Command("openssl", "x509", "-noout", "-fingerprint", "-sha256")
	cmd.Stdin = strings.NewReader(certPEM)
	out, err := cmd.Output()
	if err != nil {
		return
	}
	if want := strings.TrimSpace(strings.SplitN(string(out), "=", 2)[1]); want != fp {
		t.Fatalf("openssl says %s, the core says %s", want, fp)
	}
}

/*
A client's tunnel address is stable across reconnects, never one a live
session already holds, and never the network, the server's own or the
broadcast address.
*/
func TestAllocateIsStableAndNeverCollides(t *testing.T) {
	s, err := parseSettings(`{"subnet":"10.9.0.0/29"}`)
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
		if seen[addr] || addr == netip.MustParseAddr("10.9.0.0") || addr == netip.MustParseAddr("10.9.0.1") || addr == netip.MustParseAddr("10.9.0.7") {
			t.Fatalf("client %d got %s: taken or reserved", i, addr)
		}
		seen[addr] = true
		live = append(live, core.Session{Email: "u" + string(rune('a'+i)), Local: []netip.Addr{addr}})
	}
	if _, err := allocate(s, "one-too-many", live); err == nil {
		t.Fatal("a /29 admitted a sixth client")
	}
}

// The plugin holds the RADIUS port in four characters; the renderer refuses
// what the wire would silently mangle.
func TestRenderPluginRefusesAFiveDigitPort(t *testing.T) {
	s, _ := parseSettings(`{"subnet":"10.9.0.0/24"}`)
	p := pathsIn("/state/1")
	if _, err := renderPlugin("p-ui-openvpn-1", "127.0.0.1:18239", "127.0.0.1:1813", "s", s, p); err == nil {
		t.Fatal("a five-digit port was accepted")
	}
	cnf, err := renderPlugin("p-ui-openvpn-1", "127.0.0.1:1812", "127.0.0.1:1813", "host-secret", s, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"NAS-Identifier=p-ui-openvpn-1", "authport=1812", "acctport=1813", "sharedsecret=host-secret", "subnet=255.255.255.0", "OpenVPNConfig=" + filepath.Join("/state/1", "server.conf"), "overwriteccfiles=true"} {
		if !strings.Contains(cnf, want) {
			t.Fatalf("plugin config lacks %q:\n%s", want, cnf)
		}
	}
}

func TestRenderServerCarriesTheDesign(t *testing.T) {
	s, _ := parseSettings(`{"proto":"tcp","subnet":"10.9.0.0/24","dns":["1.1.1.1"],"redirectGateway":true}`)
	inst := core.Instance{ID: 4, Tag: "ovpn-4", Port: 1194, Listen: ""}
	conf := renderServer(inst, s, pathsIn("/state/4"), PluginPath)
	for _, want := range []string{
		"port 1194", "proto tcp", "dev pov4", "topology subnet", "server 10.9.0.0 255.255.255.0",
		"verify-client-cert none", "username-as-common-name", "ca " + filepath.Join("/state/4", "server.crt"), "tls-crypt-v2 " + filepath.Join("/state/4", "tc2-server.key"),
		"plugin " + PluginPath + " " + filepath.Join("/state/4", "radiusplugin.cnf"), "client-config-dir " + filepath.Join("/state/4", "ccd"), "management " + filepath.Join("/state/4", "mgmt.sock") + " unix",
		"disable-dco", "push \"redirect-gateway def1 bypass-dhcp\"", "push \"dhcp-option DNS 1.1.1.1\"", "status " + filepath.Join("/state/4", "status.log") + " 10",
	} {
		if !strings.Contains(conf, want) {
			t.Fatalf("server config lacks %q:\n%s", want, conf)
		}
	}
	if strings.Contains(conf, "\nuser ") || strings.Contains(conf, "explicit-exit-notify") || strings.Contains(conf, "status-version") {
		t.Fatalf("a tcp server must not carry explicit-exit-notify, no user directive belongs in the config, and the status file stays in the version the plugin reads:\n%s", conf)
	}
}

// The .ovpn is the whole client: pinned server, wrapped key, login inline.
func TestRenderClientIsACompleteProfile(t *testing.T) {
	c := New(Deps{})
	minted, err := c.MintInstance(Kind, `{"proto":"udp","subnet":"10.9.0.0/24"}`, "")
	if err != nil {
		t.Fatal(err)
	}
	inst := core.Instance{ID: 4, Tag: "ovpn-4", Port: 1194, Settings: minted}
	share, err := c.RenderClient(inst, user("alice@x", "alice"), "vpn.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if share.Kind != "file" || share.Filename != "alice.ovpn" {
		t.Fatalf("share = %s %s", share.Kind, share.Filename)
	}
	for _, want := range []string{"remote vpn.example.com 1194", "proto udp", "peer-fingerprint ", "<tls-crypt-v2>\n-----BEGIN OpenVPN tls-crypt-v2 client key-----", "<auth-user-pass>\nalice\nsecret-password\n</auth-user-pass>", "remote-cert-tls server"} {
		if !strings.Contains(share.Body, want) {
			t.Fatalf("profile lacks %q:\n%s", want, share.Body)
		}
	}
	if strings.Contains(share.Body, "<ca>") {
		t.Fatal("a pinned profile ships no CA")
	}
}

// Accounting is billed to the email, not the login name the daemon reports.
func TestSinkBillsTheEmailBehindTheUsername(t *testing.T) {
	c := New(Deps{})
	cur := &instance{ledger: radius.NewLedger("ovpn-4"), userByName: usersByName([]core.User{user("alice@x", "alice")})}
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
