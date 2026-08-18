//go:build linux

package awg

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

/*
Drives the real module, because nothing else can tell whether these attributes
are the ones it actually wants.

Skipped without the module and root: the encoder's shape is unit-tested, but a
wrong attribute number, a missing NUL terminator or a flag the kernel ignores
all produce a message that ENCODES fine and is REFUSED -- or worse, accepted and
misapplied. Only the module answers that.
*/
func requireModule(t *testing.T) *Client {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("configuring a device needs root")
	}
	client, err := New()
	if err != nil {
		if errors.Is(err, ErrModuleAbsent) {
			t.Skip("the amneziawg module is not loaded")
		}
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func makeDevice(t *testing.T, name string) {
	t.Helper()
	if out, err := exec.Command("ip", "link", "add", "dev", name, "type", LinkType).CombinedOutput(); err != nil {
		t.Fatalf("creating %s: %v (%s)", name, err, out)
	}
	t.Cleanup(func() { _ = exec.Command("ip", "link", "del", "dev", name).Run() })
}

// The whole point: the module accepts a device carrying obfuscation, and the
// values come back as they were sent.
func TestModuleAcceptsObfuscationParameters(t *testing.T) {
	client := requireModule(t)
	const name = "awgselftest0"
	makeDevice(t, name)

	key, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	port := 51999
	if err := client.ConfigureDevice(name, Config{
		PrivateKey: &key,
		ListenPort: &port,
		Params:     Params{Jc: 4, Jmin: 40, Jmax: 70},
	}); err != nil {
		t.Fatalf("ConfigureDevice: %v", err)
	}

	out, err := exec.Command("awg", "show", name).CombinedOutput()
	if err != nil {
		t.Skipf("awg tools unavailable to read the device back: %v", err)
	}
	for _, want := range []string{"jc: 4", "jmin: 40", "jmax: 70", "listening port: 51999"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("the device does not report %q:\n%s", want, out)
		}
	}
}

// A peer with allowed IPs, since a peer without them completes a handshake and
// then routes nothing at all.
func TestModuleAcceptsAPeerWithAllowedIPs(t *testing.T) {
	client := requireModule(t)
	const name = "awgselftest1"
	makeDevice(t, name)

	key, _ := wgtypes.GeneratePrivateKey()
	peerKey, _ := wgtypes.GeneratePrivateKey()
	port := 51998
	if err := client.ConfigureDevice(name, Config{
		PrivateKey:   &key,
		ListenPort:   &port,
		ReplacePeers: true,
		Peers: []Peer{{
			PublicKey:  peerKey.PublicKey(),
			AllowedIPs: []string{"10.8.0.4/32", "fd00::4/128"},
		}},
	}); err != nil {
		t.Fatalf("ConfigureDevice: %v", err)
	}

	out, err := exec.Command("awg", "show", name, "allowed-ips").CombinedOutput()
	if err != nil {
		t.Skipf("awg tools unavailable: %v", err)
	}
	for _, want := range []string{"10.8.0.4/32", "fd00::4/128"} {
		if !strings.Contains(string(out), want) {
			t.Errorf("allowed ip %q never reached the device:\n%s", want, out)
		}
	}
}

/*
Reading a device back is what a reconcile diffs against, so it has to return
what was actually set -- obfuscation included.

wgctrl would return this device's WireGuard half and silently omit every
AmneziaWG attribute, which is the failure this whole package exists to avoid:
the reconcile would see parameters missing on every pass and rewrite them
forever, rekeying every client each time.
*/
func TestDeviceReadsBackWhatWasConfigured(t *testing.T) {
	client := requireModule(t)
	const name = "awgselftest2"
	makeDevice(t, name)

	key, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	peerKey, _ := wgtypes.GeneratePrivateKey()
	port := 51997
	want := Params{Jc: 3, Jmin: 50, Jmax: 90, S1: 15, S2: 25}

	if err := client.ConfigureDevice(name, Config{
		PrivateKey: &key, ListenPort: &port, ReplacePeers: true,
		Params: want,
		Peers: []Peer{{
			PublicKey:  peerKey.PublicKey(),
			AllowedIPs: []string{"10.9.0.7/32"},
		}},
	}); err != nil {
		t.Fatalf("ConfigureDevice: %v", err)
	}

	device, err := client.Device(name)
	if err != nil {
		t.Fatalf("Device: %v", err)
	}
	// The module reports the headers it is actually using, so an unset one comes
	// back as WireGuard's default rather than as zero. Comparing without this is
	// how a reconcile would see drift forever.
	if device.Params != want.WithDefaultHeaders() {
		t.Errorf("params read back as %+v, want %+v", device.Params, want.WithDefaultHeaders())
	}
	if device.ListenPort != port {
		t.Errorf("listen port = %d, want %d", device.ListenPort, port)
	}
	if len(device.Peers) != 1 {
		t.Fatalf("got %d peers, want 1", len(device.Peers))
	}
	if device.Peers[0].PublicKey != peerKey.PublicKey() {
		t.Error("the peer that came back is not the one that was configured")
	}
	// Never contacted, so it must read as never -- not as 1970.
	if !device.Peers[0].LastHandshakeTime.IsZero() {
		t.Errorf("a peer that never handshook reports %v", device.Peers[0].LastHandshakeTime)
	}
}

// Many peers arrive across several replies, and a caller wanting one device must
// get all of them rather than the first message's worth.
func TestDeviceCoalescesManyPeers(t *testing.T) {
	client := requireModule(t)
	const name = "awgselftest3"
	makeDevice(t, name)

	key, _ := wgtypes.GeneratePrivateKey()
	const clients = 300
	peers := make([]Peer, clients)
	for i := range peers {
		peerKey, _ := wgtypes.GeneratePrivateKey()
		peers[i] = Peer{
			PublicKey:  peerKey.PublicKey(),
			AllowedIPs: []string{fmt.Sprintf("10.9.%d.%d/32", i/250, i%250)},
		}
	}
	if err := client.ConfigureDevice(name, Config{
		PrivateKey: &key, ReplacePeers: true, Peers: peers,
	}); err != nil {
		t.Fatalf("ConfigureDevice with %d peers: %v", clients, err)
	}

	device, err := client.Device(name)
	if err != nil {
		t.Fatalf("Device: %v", err)
	}
	if len(device.Peers) != clients {
		t.Fatalf("the device reports %d peers, want %d -- peers were lost in the split or the coalesce", len(device.Peers), clients)
	}
}
