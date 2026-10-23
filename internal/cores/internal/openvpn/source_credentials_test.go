package openvpn

import (
	"encoding/pem"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

func TestSourceCredentialsOpenVPNNeverInvokesHostKeyGeneration(t *testing.T) {
	dir := t.TempDir()
	blocked := filepath.Join(dir, "not-a-directory")
	if err := os.WriteFile(blocked, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, variable := range []string{"TMPDIR", "TMP", "TEMP"} {
		t.Setenv(variable, blocked)
	}
	c := New(Deps{Binary: filepath.Join(blocked, "must-not-execute"), StateDir: filepath.Join(blocked, "must-not-write")})
	for _, settings := range []string{"", `{"tlsCryptV2ServerKey":"server-material-present"}`} {
		result, err := c.MintSourceClientCredentials(Kind, settings, nil)
		if err != nil || len(result.NodeDerived) != 1 || result.NodeDerived[0] != core.CredTLSCryptV2 || result.Values[core.CredTLSCryptV2] != "" {
			t.Fatalf("source mint used host dependencies or lost pending key: fields=%d dependencies=%v err=%v", len(result.Values), result.NodeDerived, err)
		}
		if err := c.ValidateSourceClientCredentials(Kind, settings, "source", result.Values); err != nil {
			t.Fatal(err)
		}
	}
	wrapped := string(pem.EncodeToMemory(&pem.Block{Type: "OpenVPN tls-crypt-v2 client key", Bytes: []byte{1, 2, 3}}))
	have := map[string]string{core.CredUsername: "alice", core.CredPassword: "existing-password", core.CredTLSCryptV2: wrapped}
	before := maps.Clone(have)
	result, err := c.MintSourceClientCredentials(Kind, `{"tlsCryptV2ServerKey":"another-server"}`, have)
	if err != nil || len(result.Values) != 0 || len(result.NodeDerived) != 0 || !maps.Equal(have, before) {
		t.Fatalf("supplied wrapped material changed: err=%v", err)
	}
	if err := c.ValidateSourceClientCredentials(Kind, "", "source", have); err != nil {
		t.Fatalf("structural wrapped key validation failed: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || entries[0].Name() != "not-a-directory" {
		t.Fatalf("source mint wrote host files: count=%d err=%v", len(entries), err)
	}
	if _, err := c.MintClientCredentials(Kind, `{"tlsCryptV2ServerKey":"server-material-present"}`, nil); err == nil {
		t.Fatal("host trap did not catch legacy daemon-backed minting")
	}
}
