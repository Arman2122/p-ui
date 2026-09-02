package amneziawg

import (
	"context"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/awg"
	"github.com/Arman2122/p-ui/internal/core"
)

/*
The obfuscation parameters reach the module when they or the device change, not every pass.

Reconcile runs every 10 seconds and applyParams issued a SET_DEVICE with the
full parameter set each time, for every AWG inbound, whether anything had
changed -- a netlink round-trip per inbound per pass on a device whose
parameters last changed when the operator saved the form. A recreated device
starts blank and must get them again; changed parameters must land. F270.
*/
func TestObfuscationIsPushedOnlyWhenTheDeviceOrParamsChange(t *testing.T) {
	calls := 0
	previous := configureParams
	configureParams = func(string, awg.Params) error { calls++; return nil }
	t.Cleanup(func() { configureParams = previous })

	r := newRig()
	c := newTestCore(r.kernel, r.mgr)
	inst := r.instance(1)
	inst.Settings = strings.Replace(inst.Settings, `"mtu":1420,`, `"mtu":1420,"awg":{"jc":4,"jmin":10,"jmax":50},`, 1)
	ctx := context.Background()
	reconcile := func(step string) {
		t.Helper()
		if err := c.Reconcile(ctx, []core.Instance{inst}); err != nil {
			t.Fatalf("%s: %v", step, err)
		}
	}

	for range 3 {
		reconcile("steady state")
	}
	if calls != 1 {
		t.Fatalf("module calls after three identical passes = %d, want 1", calls)
	}

	r.kernel.RecreateLink(r.mgr.Name(inst.ID))
	reconcile("after the device was recreated")
	if calls != 2 {
		t.Fatalf("module calls after a recreated device = %d, want 2: a fresh device starts without the parameters", calls)
	}

	inst.Settings = strings.Replace(inst.Settings, `"jc":4`, `"jc":5`, 1)
	reconcile("after the parameters changed")
	if calls != 3 {
		t.Fatalf("module calls after changed parameters = %d, want 3", calls)
	}
}
