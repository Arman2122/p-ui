package mtproto

import (
	"os"
	"testing"

	"github.com/Arman2122/p-ui/internal/node/inventory"
)

func TestRuntimeCapabilityDoesNotStartProxyOrGenerateAssets(t *testing.T) {
	directory := t.TempDir()
	runtime := &Managed{stateDir: directory}
	capability, err := runtime.Capability(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	version, err := inventory.RunningVersion(t.Context())
	if err != nil || capability.Version != version || capability.CoreId != "mtproto" || len(capability.Protocols) != 1 || capability.Protocols[0] != "mtproto" || !capability.ActivationAdmission || !capability.ClientQuotaEnforcement || !capability.ClientRateEnforcement || len(runtime.instances) != 0 {
		t.Fatalf("invalid or mutating MTProto probe: %+v %v", capability, err)
	}
	files, err := os.ReadDir(directory)
	if err != nil || len(files) != 0 {
		t.Fatalf("capability probe generated assets: %v %v", files, err)
	}
}
