//go:build linux

package amneziawg

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/awg"
	"github.com/Arman2122/p-ui/internal/core"
)

/*
Clearing EVERY parameter is the case the early return swallowed.

applyParams used to return before touching the kernel whenever the whole struct
was zero, which is exactly the edit that matters: an operator who applied the
preset and then went back to plain WireGuard left the device padding forever,
while every .conf the panel handed out had dropped it. The other half — a
partial clear — never reached the kernel either, but for a different reason, in
the encoder.
*/
func TestApplyParamsClearsAnInboundBackToPlainWireguard(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("configuring a device needs root")
	}
	if _, err := exec.LookPath("awg"); err != nil {
		t.Skip("the awg tools are not installed")
	}

	c := New()
	inst := core.Instance{ID: 4242, Kind: Kind}
	name := c.mgr.Name(inst.ID)
	if out, err := exec.Command("ip", "link", "add", "dev", name, "type", awg.LinkType).CombinedOutput(); err != nil {
		t.Skipf("creating %s: %v (%s)", name, err, out)
	}
	t.Cleanup(func() { _ = exec.Command("ip", "link", "del", "dev", name).Run() })

	settings, err := json.Marshal(map[string]any{
		"awg": map[string]any{"jc": 4, "jmin": 40, "jmax": 70, "s1": 20, "s2": 30},
	})
	if err != nil {
		t.Fatal(err)
	}
	inst.Settings = string(settings)
	if err := c.applyParams(context.Background(), inst); err != nil {
		t.Fatalf("apply the preset: %v", err)
	}
	if before := awgShowDevice(t, name); !strings.Contains(before, "s1: 20") {
		t.Fatalf("the preset did not land; the rest proves nothing:\n%s", before)
	}

	// Back to plain WireGuard: every parameter cleared in the form.
	cleared, err := json.Marshal(map[string]any{"awg": map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	inst.Settings = string(cleared)
	if err := c.applyParams(context.Background(), inst); err != nil {
		t.Fatalf("apply the cleared parameters: %v", err)
	}

	after := awgShowDevice(t, name)
	for _, gone := range []string{"jc: 4", "jmin: 40", "jmax: 70", "s1: 20", "s2: 30"} {
		if strings.Contains(after, gone) {
			t.Errorf("%q survived a full clear: the device still obfuscates and every client's .conf no longer does\n%s", gone, after)
		}
	}
}

func awgShowDevice(t *testing.T, name string) string {
	t.Helper()
	out, err := exec.Command("awg", "show", name).CombinedOutput()
	if err != nil {
		t.Skipf("awg show: %v", err)
	}
	return string(out)
}
