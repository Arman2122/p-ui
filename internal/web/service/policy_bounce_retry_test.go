package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/runtime"
)

// bounceFixture seeds one local Xray inbound and points the runtime at fake.
func bounceFixture(t *testing.T, tag string, port int, fake *fakeNodeRuntime) *model.Inbound {
	t.Helper()
	setupConflictDB(t)

	mgr := runtime.NewManager(runtime.LocalDeps{Cores: testCores(t)})
	mgr.SetLocalRuntimeOverride(fake)
	runtime.SetManager(mgr)
	t.Cleanup(func() { runtime.SetManager(nil) })

	seedInboundConflict(t, tag, "", port, model.VLESS, "",
		`{"clients":[{"email":"capped","id":"3a3d2f6e-6f1a-4b7c-9d2e-1f0a5c8b7d70","enable":true}],"decryption":"none"}`)
	return loadInboundByTag(t, tag)
}

/*
A bounce is remove-then-add, and the add is the half that matters.

The remove is the cut the IP cap asks for; the add is what makes it a cut rather
than a revocation. A single transient failure on the way back in — the same
busy core the remove just poked — used to leave the client removed from the
running config with nothing to put them back: the supervisor skips a
panel-converged core, so the client stayed off until an unrelated edit rebuilt
the config. Exceeding an IP limit is not supposed to delete an account.
*/
func TestABounceThatCannotReAddImmediatelyKeepsTrying(t *testing.T) {
	fake := &fakeNodeRuntime{}
	fake.addUserTransientFailures.Store(2)
	inbound := bounceFixture(t, "vless-bounce-retry", 46180, fake)

	if err := (&PolicyService{}).BounceClient(context.Background(), inbound, "capped"); err != nil {
		t.Fatalf("BounceClient: %v — a transient failure on the way back in is not a reason to leave the client cut off", err)
	}
	if n := fake.removeUser.Load(); n != 1 {
		t.Fatalf("RemoveUser ran %d time(s), want 1: the cut is what the cap asks for", n)
	}
	if n := fake.addUser.Load(); n != 3 {
		t.Fatalf("AddUser ran %d time(s), want 3: two transient failures then the one that lands", n)
	}
	if n := fake.updateInbound.Load(); n != 0 {
		t.Fatalf("the whole inbound was re-applied %d time(s) after a successful re-add; that repair is for the case where every attempt failed", n)
	}
}

/*
When every attempt fails the client really is absent, and the last thing left is
to rebuild the inbound's user list from the panel's own config — a stronger
repair than a fourth AddUser of the same user. The error still surfaces either
way, because a bounce that needed the repair is not a healthy bounce.
*/
func TestABounceThatCanNeverReAddRepairsTheWholeInbound(t *testing.T) {
	fake := &fakeNodeRuntime{addUserErr: errors.New("the core refused the user")}
	inbound := bounceFixture(t, "vless-bounce-dead", 46181, fake)

	err := (&PolicyService{}).BounceClient(context.Background(), inbound, "capped")
	if err == nil {
		t.Fatal("every re-add failed and BounceClient reported success")
	}
	if n := fake.addUser.Load(); n != bounceAddAttempts {
		t.Fatalf("AddUser ran %d time(s), want the full budget of %d", n, bounceAddAttempts)
	}
	if n := fake.updateInbound.Load(); n != 1 {
		t.Fatalf("the inbound was re-applied %d time(s), want 1: nothing else puts the client back", n)
	}
	if !strings.Contains(err.Error(), "capped") {
		t.Fatalf("the error must name the client left cut off, got %q", err)
	}
}
