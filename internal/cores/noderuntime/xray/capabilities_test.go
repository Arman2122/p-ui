package xrayruntime

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/Arman2122/p-ui/internal/node/inventory"
)

func TestRuntimeCapabilityProbesPatchedEngineWithoutStartingIt(t *testing.T) {
	runtime := new(Runtime)
	capability, err := runtime.Capability(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	version, err := inventory.RunningVersion(t.Context())
	if err != nil || capability.Version != version || capability.CoreId != "xray" || !slices.Equal(capability.Protocols, []string{"http", "hysteria", "mixed", "shadowsocks", "trojan", "vless", "vmess"}) || !capability.ClientQuotaEnforcement || !capability.ClientRateEnforcement || !capability.ActivationAdmission || runtime.instance != nil || runtime.authentication != nil || len(runtime.configuration) != 0 {
		t.Fatalf("invalid or mutating Xray probe: %+v %v", capability, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if capability, err := runtime.Capability(ctx); !errors.Is(err, context.Canceled) || capability != nil {
		t.Fatalf("canceled probe advertised Xray: %+v %v", capability, err)
	}
}
