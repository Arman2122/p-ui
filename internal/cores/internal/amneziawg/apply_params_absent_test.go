package amneziawg

import (
	"context"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	engine "github.com/Arman2122/p-ui/internal/wireguard"
)

// absentPlane answers the presence question and nothing else. Every other Plane
// method is left nil on purpose: reaching one is a test failure, loudly.
type absentPlane struct {
	engine.Plane
	exists bool
	asked  []string
}

func (p *absentPlane) Snapshot(_ context.Context, name string) (engine.Snapshot, error) {
	p.asked = append(p.asked, name)
	return engine.Snapshot{Exists: p.exists}, nil
}

/*
A device that has not been created yet must not be reported as a fault.

The module answers a missing device with EINVAL, byte for byte the errno it uses
for a parameter set it refuses, so applyParams reported both — and a device
still being created poisoned the joined result of every supervise pass. The
presence question belongs to the plane, which can answer it without an error.
Ledger L3.
*/
func TestObfuscationIsNotPushedToADeviceThatIsNotThere(t *testing.T) {
	plane := &absentPlane{exists: false}
	c := &Core{mgr: engine.NewNamedManager(plane, DevicePrefix), plane: plane}

	inst := core.Instance{ID: 7, Kind: Kind, Settings: `{"awg":{"jc":4}}`}
	if err := c.applyParams(context.Background(), inst); err != nil {
		t.Fatalf("an absent device is not a fault, got: %v", err)
	}
	if len(plane.asked) != 1 || plane.asked[0] != c.mgr.Name(7) {
		t.Fatalf("applyParams must ask the plane about this inbound's own device, asked: %v", plane.asked)
	}
}

// And a device that IS there is still configured — the guard must not have
// turned every push into a no-op.
func TestObfuscationStillReachesADeviceThatExists(t *testing.T) {
	plane := &absentPlane{exists: true}
	c := &Core{mgr: engine.NewNamedManager(plane, DevicePrefix), plane: plane}

	inst := core.Instance{ID: 7, Kind: Kind, Settings: `{"awg":{"jc":4}}`}
	err := c.applyParams(context.Background(), inst)
	if err == nil {
		t.Skip("this host has a live pawg7 device; the interesting case is the failure below")
	}
	// There is no real device behind the stub, so the configure must have been
	// attempted and failed — which is what proves the guard let it through.
	if !strings.Contains(err.Error(), "obfuscation") {
		t.Fatalf("the error should come from the configure attempt, got: %v", err)
	}
}
