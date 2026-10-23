package sshruntime

import (
	"os"
	"testing"

	"github.com/Arman2122/p-ui/internal/node/inventory"
)

func TestRuntimeCapabilityDoesNotGenerateSSHIdentity(t *testing.T) {
	directory := t.TempDir()
	runtime := &Runtime{directory: directory}
	capability, err := runtime.Capability(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	version, err := inventory.RunningVersion(t.Context())
	if err != nil || capability.Version != version || capability.CoreId != "ssh" || len(capability.Protocols) != 1 || capability.Protocols[0] != "ssh" || !capability.ActivationAdmission || !capability.ClientQuotaEnforcement || !capability.ClientRateEnforcement || runtime.instance != nil || runtime.hostKey != "" {
		t.Fatalf("invalid or mutating SSH probe: %+v %v", capability, err)
	}
	files, err := os.ReadDir(directory)
	if err != nil || len(files) != 0 {
		t.Fatalf("capability probe generated a host identity: %v %v", files, err)
	}
}
