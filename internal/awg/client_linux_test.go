//go:build linux

package awg

import (
	"errors"
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
