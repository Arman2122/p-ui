//go:build linux

package amneziawg

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	wgutil "github.com/Arman2122/p-ui/internal/util/wireguard"
)

/*
One inbound the pass cannot parse must not cost every sibling its obfuscation.

Reconcile early-returned on the WireGuard core's error, and that error is a
join over EVERY desired inbound — one missing secretKey, one bad address, one
failed EnsureLink. So a single broken sibling meant applyParams never ran for
any AmneziaWG inbound in the pass, and because the next pass took the same exit,
the healthy devices came up as plain kernel WireGuard and stayed that way for as
long as the co-failure lasted. Their clients are configured for AmneziaWG, so
none of them can connect, and the panel reports the devices as converged.
*/
func TestASiblingThatCannotBeParsedDoesNotStripTheRestOfTheirObfuscation(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("configuring a device needs root")
	}
	if _, err := exec.LookPath("awg"); err != nil {
		t.Skip("the awg tools are not installed")
	}

	key, _, err := wgutil.GenerateWireguardKeypair()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	// No secretKey at all: toEngine refuses it, so the WireGuard core's joined
	// error is non-nil for the whole pass. This is the sibling.
	broken := core.Instance{ID: 4251, Kind: Kind, Enable: true, Port: 51851}
	brokenSettings, err := json.Marshal(map[string]any{"awg": map[string]any{"jc": 4, "jmin": 40, "jmax": 70}})
	if err != nil {
		t.Fatal(err)
	}
	broken.Settings = string(brokenSettings)

	healthy := core.Instance{ID: 4252, Kind: Kind, Enable: true, Port: 51852}
	healthySettings, err := json.Marshal(map[string]any{
		"secretKey": key,
		"address":   []string{"10.77.52.1/24"},
		"awg":       map[string]any{"jc": 7, "jmin": 41, "jmax": 71},
	})
	if err != nil {
		t.Fatal(err)
	}
	healthy.Settings = string(healthySettings)

	c := New()
	brokenName, healthyName := c.mgr.Name(broken.ID), c.mgr.Name(healthy.ID)
	t.Cleanup(func() {
		_ = exec.Command("ip", "link", "del", "dev", brokenName).Run()
		_ = exec.Command("ip", "link", "del", "dev", healthyName).Run()
	})

	// The pass fails, and it is supposed to: the broken sibling is a real error.
	if err := c.Reconcile(context.Background(), []core.Instance{broken, healthy}); err == nil {
		t.Fatal("an inbound with no secretKey was reconciled without error; this test proves nothing unless the pass actually fails")
	}

	out, err := exec.Command("awg", "show", healthyName).CombinedOutput()
	if err != nil {
		t.Skipf("the healthy device was not created (%v): %s", err, out)
	}
	if !strings.Contains(string(out), "jc: 7") {
		t.Fatalf("the healthy inbound came up as plain WireGuard because a sibling failed:\n%s", out)
	}
}
