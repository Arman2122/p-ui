package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

type fleetDispatchProbe struct {
	Runtime
	email string
	calls int
}

func (p *fleetDispatchProbe) AddUser(_ context.Context, _ *model.Inbound, email string) error {
	p.email, p.calls = email, p.calls+1
	return nil
}

func TestFleetRoleCannotFallBackToLegacyExecution(t *testing.T) {
	probe := new(fleetDispatchProbe)
	manager, err := NewFleetManager(probe)
	if err != nil {
		t.Fatal(err)
	}
	manager.SetLocalRuntimeOverride(NewLocal(LocalDeps{}))
	manager.SetRuntimeOverride(1, NewLocal(LocalDeps{}))
	dispatch, err := manager.RuntimeFor(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatch.AddUser(t.Context(), &model.Inbound{Id: 7}, "managed-client"); err != nil {
		t.Fatal(err)
	}
	if probe.calls != 1 || probe.email != "managed-client" {
		t.Fatalf("intent did not reach fleet dispatcher: %+v", probe)
	}
	legacyID := 1
	if _, err := manager.RuntimeFor(&legacyID); !errors.Is(err, ErrLegacyRuntime) {
		t.Fatalf("legacy identifier selected a runtime: %v", err)
	}
	if _, err := manager.RemoteFor(&model.Node{Id: 1, Enable: true}); !errors.Is(err, ErrLegacyRuntime) {
		t.Fatalf("legacy remote was constructed: %v", err)
	}
	if _, err := NewFleetManager(nil); !errors.Is(err, ErrFleetDispatcher) {
		t.Fatalf("missing fleet dispatcher accepted: %v", err)
	}
}
