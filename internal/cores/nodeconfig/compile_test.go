package nodeconfig

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/core"
)

func TestCompilerDetectsCrossCoreListenerCollision(t *testing.T) {
	input := xrayCompilerFixture(t)
	input.SSHHostKeys = HostKeyBinding{GenerateLocally: true}
	input.Inbounds = append(input.Inbounds, Inbound{Instance: core.Instance{ID: 2, Kind: "ssh", Listen: "127.0.0.1", Port: input.Inbounds[0].Instance.Port, Enable: true, Settings: `{"transport":"tcp"}`}, TargetID: 2, TargetTag: "ssh", Clients: []Client{{ClientID: uuid.NewString(), User: core.User{Email: "ssh-label", Enable: true, Credentials: map[string]any{core.CredUsername: "alice", core.CredPassword: "assigned-password"}}}}})
	input.Inbounds[1].Egress = EgressBinding{Mode: EgressDirect}
	if _, err := Compile(input); !errors.Is(err, ErrConflict) {
		t.Fatalf("cross-core listener collision accepted: %v", err)
	}
	input.Inbounds[1].Instance.Port++
	result, err := Compile(input)
	if err != nil || len(result.Runtimes) != 2 || len(result.Listeners) != 2 {
		t.Fatalf("independent node graph: runtimes=%d listeners=%d err=%v", len(result.Runtimes), len(result.Listeners), err)
	}
}
