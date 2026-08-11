package egress_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Arman2122/p-ui/internal/egress"
	"github.com/Arman2122/p-ui/internal/egress/egtest"
)

/*
Listing rules is an unprivileged read. A host that allows every read and refuses
every write is exactly the shape a capability-restricted unit has, and Preflight
is built on Probe: passing one hands the operator an unrouted attach whose error
names neither the capability nor the remedy.
*/
func TestProbeRefusesAHostThatCanReadButNotWrite(t *testing.T) {
	k := egtest.New()
	k.Fail = map[string]error{
		"rule+ " + egress.ProbeRule().String(): fmt.Errorf("%w: operation not permitted", egress.ErrPermission),
	}
	m := egress.New(k, nil)

	report := m.Preflight(t.Context(), egress.DefaultGatewayBase)
	if !errors.Is(report.Err(), egress.ErrPermission) {
		t.Fatalf("Preflight refusals = %v, want ErrPermission naming CAP_NET_ADMIN", report.Err())
	}
}

// The probe has to leave the host as it found it, or it becomes the foreign
// object the next preflight refuses.
func TestProbeLeavesNothingBehind(t *testing.T) {
	k := egtest.New()
	m := egress.New(k, nil)

	if report := m.Preflight(t.Context(), egress.DefaultGatewayBase); !report.OK() {
		t.Fatalf("Preflight refused a clean host: %v", report.Err())
	}
	if rules := k.Rules(); len(rules) != 0 {
		t.Fatalf("the probe left %v behind", rules)
	}
	if got, want := k.Ops()[:2], []string{"rule+ " + egress.ProbeRule().String(), "rule- " + egress.ProbeRule().String()}; got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("probe ops = %v, want the write and its undo", got)
	}
}

// A rule the panel did not add is not the panel's to delete, and EEXIST comes
// from the kernel after its own capability check, so the write path is proven.
func TestProbeDoesNotDeleteSomebodyElsesRule(t *testing.T) {
	k := egtest.New()
	k.Fail = map[string]error{
		"rule+ " + egress.ProbeRule().String(): fmt.Errorf("%w: file exists", egress.ErrAlreadyInstalled),
	}
	m := egress.New(k, nil)

	if report := m.Preflight(t.Context(), egress.DefaultGatewayBase); !report.OK() {
		t.Fatalf("Preflight = %v, want an occupied probe slot to prove the write path", report.Err())
	}
	if ops := k.Ops(); len(ops) == 0 || ops[0] != "rule+ "+egress.ProbeRule().String() {
		t.Fatalf("probe ops = %v, want the write to have been attempted", ops)
	}
	for _, op := range k.Ops() {
		if op == "rule- "+egress.ProbeRule().String() {
			t.Fatalf("the probe deleted a rule it did not install: %v", k.Ops())
		}
	}
}
