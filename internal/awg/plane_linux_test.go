//go:build linux

package awg

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/Arman2122/p-ui/internal/wireguard"
)

func requirePlane(t *testing.T) wireguard.Plane {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("creating a device needs root")
	}
	plane := NewPlane()
	if err := plane.Probe(context.Background()); err != nil {
		if errors.Is(err, ErrModuleAbsent) {
			t.Skip("the amneziawg module is not loaded")
		}
		t.Fatalf("Probe: %v", err)
	}
	return plane
}

/*
The whole reason this Plane exists: the manager that already reconciles kernel
WireGuard devices must drive an AmneziaWG one without knowing it is one.

If any of link creation, address, route or peer handling needed a second
implementation, this would be a second manager instead -- and two reconcile
loops for one job is how the two of them drift apart.
*/
func TestPlaneCreatesAndConfiguresAnAmneziawgDevice(t *testing.T) {
	plane := requirePlane(t)
	ctx := context.Background()
	const name = "awgplane0"
	t.Cleanup(func() { _ = plane.DeleteLink(ctx, name) })

	state, err := plane.EnsureLink(ctx, wireguard.LinkSpec{Name: name, MTU: 1420})
	if err != nil {
		t.Fatalf("EnsureLink: %v", err)
	}
	if !state.Created || !state.Up {
		t.Fatalf("link state = %+v, want created and up", state)
	}

	// The kind matters: a device of the wrong type would take addresses and
	// routes happily and then refuse every configuration.
	out, err := exec.Command("ip", "-d", "link", "show", name).CombinedOutput()
	if err != nil {
		t.Fatalf("reading the link: %v (%s)", err, out)
	}
	if !containsString(string(out), LinkType) {
		t.Fatalf("device is not an %s link:\n%s", LinkType, out)
	}

	key, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	port := 51996
	if err := plane.Configure(ctx, name, wgtypes.Config{PrivateKey: &key, ListenPort: &port}); err != nil {
		t.Fatalf("Configure: %v", err)
	}

	snap, err := plane.Snapshot(ctx, name)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !snap.Exists {
		t.Fatal("the device the plane just created does not exist")
	}
	if snap.Device.ListenPort != port {
		t.Errorf("listen port = %d, want %d", snap.Device.ListenPort, port)
	}
	if snap.Device.PrivateKey != key {
		t.Error("the key read back is not the one configured")
	}
}

/*
Obfuscation applies without disturbing the peers.

A parameter edit must not be an excuse to rewrite the peer list: doing so on a
live inbound would drop every client's session for a change none of them needed.
*/
func TestConfigureParamsLeavesPeersAlone(t *testing.T) {
	plane := requirePlane(t)
	ctx := context.Background()
	const name = "awgplane1"
	t.Cleanup(func() { _ = plane.DeleteLink(ctx, name) })

	if _, err := plane.EnsureLink(ctx, wireguard.LinkSpec{Name: name, MTU: 1420}); err != nil {
		t.Fatalf("EnsureLink: %v", err)
	}
	key, _ := wgtypes.GeneratePrivateKey()
	peerKey, _ := wgtypes.GeneratePrivateKey()
	if err := plane.Configure(ctx, name, wgtypes.Config{
		PrivateKey:   &key,
		ReplacePeers: true,
		Peers:        []wgtypes.PeerConfig{{PublicKey: peerKey.PublicKey()}},
	}); err != nil {
		t.Fatalf("Configure: %v", err)
	}

	if err := ConfigureParams(name, Params{Jc: 5, Jmin: 30, Jmax: 60}); err != nil {
		t.Fatalf("ConfigureParams: %v", err)
	}

	client, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer client.Close()
	device, err := client.Device(name)
	if err != nil {
		t.Fatalf("Device: %v", err)
	}
	if device.Params.Jc != 5 || device.Params.Jmin != 30 || device.Params.Jmax != 60 {
		t.Errorf("params = %+v, want jc 5 jmin 30 jmax 60", device.Params)
	}
	if len(device.Peers) != 1 {
		t.Fatalf("the device has %d peers after a parameter change, want the 1 it started with", len(device.Peers))
	}
	if device.Peers[0].PublicKey != peerKey.PublicKey() {
		t.Error("the peer survived in name only: it is not the one configured")
	}
}

func containsString(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
