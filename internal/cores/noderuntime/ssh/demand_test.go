package sshruntime

import (
	"context"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	gossh "golang.org/x/crypto/ssh"

	"github.com/Arman2122/p-ui/internal/core"
	sshcore "github.com/Arman2122/p-ui/internal/cores/internal/ssh"
	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func TestRuntimeContextReachesOnlyAuthenticatedSSHDemand(t *testing.T) {
	keys, err := sshcore.NewNodeHostKeys()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := gossh.ParsePrivateKey([]byte(keys))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	clientID := uuid.NewString()
	config := sshcore.NodeConfiguration{HostKeyPEM: keys, Inbounds: []core.Instance{{ID: 42, Kind: sshcore.Kind, Tag: "scoped-runtime-ssh", Listen: "127.0.0.1", Port: port, Enable: true, Settings: `{}`, Users: []core.User{{Email: clientID, Enable: true, Credentials: map[string]any{core.CredUsername: "alice", core.CredPassword: "known-test-password"}}}}}}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan [2]string, 2)
	ctx := admission.WithHandler(t.Context(), func(_ context.Context, client, inbound string) error {
		events <- [2]string{client, inbound}
		return nil
	})
	process := new(Runtime)
	if err := process.Validate(encoded); err != nil {
		t.Fatal(err)
	}
	if err := process.Apply(ctx, encoded, func(string) (*limits.Account, bool) { return nil, false }); err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	for _, password := range []string{"wrong-password", "known-test-password"} {
		client, err := gossh.Dial("tcp", address, &gossh.ClientConfig{User: "alice", Auth: []gossh.AuthMethod{gossh.Password(password)}, HostKeyCallback: gossh.FixedHostKey(signer.PublicKey()), HostKeyAlgorithms: []string{signer.PublicKey().Type()}, Timeout: time.Second})
		if err == nil {
			_ = client.Close()
			t.Fatal("auth-only SSH runtime admitted an unfunded client")
		}
	}
	if len(events) != 1 || <-events != [2]string{clientID, "scoped-runtime-ssh"} {
		t.Fatal("runtime context did not bind exact authenticated SSH demand")
	}
}
