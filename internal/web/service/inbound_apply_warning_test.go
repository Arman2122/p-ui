package service

import (
	"errors"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/runtime"
)

/*
A save the core refused is not a save that succeeded.

postCommitApply runs after the transaction commits, and its failure was written
to logger.Debug and nothing else. needRestart was raised only when the NEW
protocol is Xray-served — and for a core the panel converges directly (awgkernel,
wgkernel, mtproto) the runtime push IS the apply, so there is no later restart to
raise. The operator got "Inbound has been successfully updated", a green row, and
a device still running the previous configuration.

The warning is the whole fix: the row really is saved, so this is not an error,
but nothing downstream repairs it either.
*/
func TestAnApplyTheCoreRefusedIsReportedAsAWarning(t *testing.T) {
	setupConflictDB(t)

	mgr := runtime.NewManager(runtime.LocalDeps{Cores: testCores(t)})
	fake := &fakeNodeRuntime{updateInboundErr: errors.New("the device refused the configuration")}
	mgr.SetLocalRuntimeOverride(fake)
	runtime.SetManager(mgr)
	t.Cleanup(func() { runtime.SetManager(nil) })

	seedInboundConflict(t, "awg-warn", "", 46320, model.WGKernel, "", `{"clients":[]}`)
	update := *loadInboundByTag(t, "awg-warn")
	update.Remark = "edited"

	saved, needRestart, warn, err := (&InboundService{}).UpdateInbound(&update)
	if err != nil {
		t.Fatalf("UpdateInbound: %v — the row committed, so this must not be an error", err)
	}
	if saved == nil {
		t.Fatal("no inbound returned")
	}
	if warn == "" {
		t.Fatal("the core refused the configuration and the operator was told the save succeeded")
	}
	if !strings.Contains(warn, "refused the configuration") {
		t.Fatalf("the warning must carry the core's own reason, got %q", warn)
	}
	if needRestart {
		t.Fatal("an Xray restart was scheduled for an inbound Xray does not serve; it cannot apply it and drops every other connection")
	}
}

// The Xray-served half is unchanged: a failed push there is repaired by the
// restart it schedules, so it warns about nothing.
func TestAnXrayApplyFailureStillJustSchedulesTheRestart(t *testing.T) {
	setupConflictDB(t)

	mgr := runtime.NewManager(runtime.LocalDeps{Cores: testCores(t)})
	fake := &fakeNodeRuntime{updateInboundErr: errors.New("xray refused the update")}
	mgr.SetLocalRuntimeOverride(fake)
	runtime.SetManager(mgr)
	t.Cleanup(func() { runtime.SetManager(nil) })

	seedInboundConflict(t, "vless-warn", "", 46321, model.VLESS, "",
		`{"clients":[{"email":"vw","id":"3a3d2f6e-6f1a-4b7c-9d2e-1f0a5c8b7d80","enable":true}],"decryption":"none"}`)
	update := *loadInboundByTag(t, "vless-warn")
	update.Remark = "edited"

	_, needRestart, warn, err := (&InboundService{}).UpdateInbound(&update)
	if err != nil {
		t.Fatalf("UpdateInbound: %v", err)
	}
	if !needRestart {
		t.Fatal("a failed Xray push must still schedule the restart that repairs it")
	}
	if warn != "" {
		t.Fatalf("warned %q about a failure the scheduled restart repairs; a warning nobody needs to act on is how the ones that matter get ignored", warn)
	}
}

// A save the core accepted says nothing at all.
func TestAnAppliedSaveCarriesNoWarning(t *testing.T) {
	setupConflictDB(t)

	mgr := runtime.NewManager(runtime.LocalDeps{Cores: testCores(t)})
	mgr.SetLocalRuntimeOverride(&fakeNodeRuntime{})
	runtime.SetManager(mgr)
	t.Cleanup(func() { runtime.SetManager(nil) })

	seedInboundConflict(t, "awg-ok", "", 46322, model.WGKernel, "", `{"clients":[]}`)
	update := *loadInboundByTag(t, "awg-ok")
	update.Remark = "edited"

	if _, _, warn, err := (&InboundService{}).UpdateInbound(&update); err != nil || warn != "" {
		t.Fatalf("UpdateInbound = warn %q, err %v; want both empty", warn, err)
	}
}
