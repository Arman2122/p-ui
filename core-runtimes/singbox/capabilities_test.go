package singbox

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/Arman2122/p-ui/internal/node/inventory"
)

func TestRuntimeCapabilityProbesPatchedEngineWithoutStartingIt(t *testing.T) {
	runtime := new(Managed)
	capability, err := runtime.Capability(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	version, err := inventory.RunningVersion(t.Context())
	if err != nil || capability.Version != version || capability.CoreId != "singbox" || !slices.Equal(capability.Protocols, []string{"anytls", "tuic"}) || !capability.ClientQuotaEnforcement || !capability.ClientRateEnforcement || !capability.ActivationAdmission || runtime.instance != nil || runtime.authentication != nil || len(runtime.configuration) != 0 {
		t.Fatalf("invalid or mutating sing-box probe: %+v %v", capability, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if capability, err := runtime.Capability(ctx); !errors.Is(err, context.Canceled) || capability != nil {
		t.Fatalf("canceled probe advertised sing-box: %+v %v", capability, err)
	}
}
