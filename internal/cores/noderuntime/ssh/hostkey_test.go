package sshruntime

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	gossh "golang.org/x/crypto/ssh"

	"github.com/Arman2122/p-ui/internal/core"
	sshcore "github.com/Arman2122/p-ui/internal/cores/internal/ssh"
	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/node/state"
)

func TestNodeLocalSSHIdentitySurvivesRestartAndRefusesSilentReplacement(t *testing.T) {
	directory := t.TempDir()
	identity := state.Identity{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()}
	store, err := state.Open(filepath.Join(directory, "runtime.db"), identity)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	address := listener.Addr().String()
	_ = listener.Close()
	clientID := uuid.NewString()
	config := sshcore.NodeConfiguration{Inbounds: []core.Instance{{
		ID: 1, Kind: sshcore.Kind, Tag: "node-ssh", Listen: "127.0.0.1", Port: port, Enable: true, Settings: "{}",
		Users: []core.User{{Email: clientID, Enable: true, Credentials: map[string]any{core.CredUsername: "alice", core.CredPassword: "test-only-password"}}},
	}}}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	account, err := limits.New(limits.Policy{
		Term: "ssh-local", Revision: 1, Enabled: true, QuotaUnlimited: true,
		Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited},
	})
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(id string) (*limits.Account, bool) { return account, id == clientID }
	connect := func() string {
		t.Helper()
		fingerprint := ""
		client, err := gossh.Dial("tcp", address, &gossh.ClientConfig{
			User: "alice", Auth: []gossh.AuthMethod{gossh.Password("test-only-password")}, Timeout: 3 * time.Second,
			HostKeyCallback: func(_ string, _ net.Addr, key gossh.PublicKey) error {
				fingerprint = gossh.FingerprintSHA256(key)
				return nil
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		_ = client.Close()
		if fingerprint == "" {
			t.Fatal("SSH host identity was not negotiated")
		}
		return fingerprint
	}
	process := new(Runtime)
	process.SetStateStore(directory, store)
	if err := process.Validate(encoded); err != nil {
		t.Fatal(err)
	}
	if err := process.Apply(t.Context(), encoded, lookup); err != nil {
		t.Fatal(err)
	}
	first := connect()
	if err := process.Close(); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = state.Open(filepath.Join(directory, "runtime.db"), identity)
	if err != nil {
		t.Fatal(err)
	}
	process = new(Runtime)
	process.SetStateStore(directory, store)
	defer process.Close()
	if err := process.Apply(t.Context(), encoded, lookup); err != nil {
		t.Fatal(err)
	}
	if got := connect(); got != first {
		t.Fatalf("SSH identity changed across restart: %s != %s", got, first)
	}
	if err := process.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(directory, "host-keys.pem")); err != nil {
		t.Fatal(err)
	}
	if err := process.Apply(t.Context(), encoded, lookup); !errors.Is(err, state.ErrIdentity) {
		t.Fatalf("missing key was regenerated: %v", err)
	}
	replacement, err := sshcore.NewNodeHostKeys()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "host-keys.pem"), []byte(replacement), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := process.Apply(t.Context(), encoded, lookup); !errors.Is(err, state.ErrIdentity) {
		t.Fatalf("changed key was silently accepted: %v", err)
	}
	config.HostKeyPEM = replacement
	encoded, err = json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Apply(t.Context(), encoded, lookup); err != nil {
		t.Fatal(err)
	}
	if got := connect(); got == first {
		t.Fatal("explicit host-key rotation was ignored")
	}
}
