package job

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Arman2122/p-ui/internal/egress"
)

// A host fact answers identically on every 10s tick forever, so it belongs at
// debug; anything the next pass might repair has to stay an alarm.
func TestEgressReconcileSeparatesHostFactsFromDrift(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"platform", fmt.Errorf("egress 1: %w", egress.ErrPlatformUnsupported), true},
		{"permission", fmt.Errorf("egress 1: %w", egress.ErrPermission), true},
		{"family the kernel does not carry", errors.Join(
			fmt.Errorf("egress: contain v6 table 30001 blackhole default metric 4096: %w", egress.ErrFamilyUnsupported),
			errors.New("egress: refusing v6 prio 31001 iif pwg3 lookup 30001: its table has no blackhole"),
		), true},
		{"a front slot somebody else took", fmt.Errorf("egress 1: %w", egress.ErrAlreadyInstalled), false},
		{"a foreign object in the band", fmt.Errorf("egress 1: %w", egress.ErrForeignResource), false},
		{"the device is not up yet", fmt.Errorf("egress 1: %w", egress.ErrNoDevice), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := egressReconcileIsAHostFact(tc.err); got != tc.want {
				t.Fatalf("egressReconcileIsAHostFact(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
