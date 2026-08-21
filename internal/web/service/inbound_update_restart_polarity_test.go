package service

import (
	"errors"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/runtime"
)

// TestUpdateInboundFailedPushSkipsRestartForSelfConvergingCore pins the polarity
// of the restart decision: restarting Xray cannot apply an inbound it never serves.
func TestUpdateInboundFailedPushSkipsRestartForSelfConvergingCore(t *testing.T) {
	setupConflictDB(t)

	mgr := runtime.NewManager(runtime.LocalDeps{Cores: testCores(t)})
	fake := &fakeNodeRuntime{updateInboundErr: errors.New("sidecar refused the update")}
	mgr.SetLocalRuntimeOverride(fake)
	runtime.SetManager(mgr)
	t.Cleanup(func() { runtime.SetManager(nil) })

	seedInboundConflict(t, "mt-polarity", "", 46170, model.MTProto, "",
		`{"clients":[{"email":"mtp","secret":"`+mtprotoTestSecretA+`","enable":true}]}`)
	seeded := loadInboundByTag(t, "mt-polarity")
	seedClientTraffic(t, seeded.Id, "mtp", true)

	update := *loadInboundByTag(t, "mt-polarity")
	update.Remark = "edited"
	_, needRestart, err := (&InboundService{}).UpdateInbound(&update)
	if err != nil {
		t.Fatalf("UpdateInbound: %v", err)
	}

	if n := fake.updateInbound.Load(); n != 1 {
		t.Fatalf("in-place push ran %d time(s), want 1 — the failure path this test is about never ran", n)
	}
	if needRestart {
		t.Fatal("a failed MTProto push asked for an Xray restart; Xray does not serve the inbound, so the restart drops every other connection and changes nothing")
	}
}

// TestAddClientFailedPushSkipsRestartForSelfConvergingCore is the same polarity
// one layer down: adding a client to a wgkernel inbound dispatches AddUser, and
// a failure there must not schedule an Xray restart either.
func TestAddClientFailedPushSkipsRestartForSelfConvergingCore(t *testing.T) {
	setupConflictDB(t)

	mgr := runtime.NewManager(runtime.LocalDeps{Cores: testCores(t)})
	fake := &fakeNodeRuntime{addUserErr: errors.New("the device refused the peer")}
	mgr.SetLocalRuntimeOverride(fake)
	runtime.SetManager(mgr)
	t.Cleanup(func() { runtime.SetManager(nil) })

	inboundSvc := &InboundService{}
	created, _, err := inboundSvc.AddInbound(wgkernelInbound(46172, "10.20.0.1/24"))
	if err != nil {
		t.Fatalf("AddInbound: %v", err)
	}

	add := &model.Inbound{Id: created.Id, Protocol: model.WGKernel, Settings: clientsSettings(t, []model.Client{
		{Email: "peer@wgk", Enable: true},
	})}
	needRestart, err := (&ClientService{}).AddInboundClient(inboundSvc, add)
	if err != nil {
		t.Fatalf("AddInboundClient: %v", err)
	}
	if needRestart {
		t.Fatal("a failed wgkernel peer push asked for an Xray restart; Xray does not serve the inbound, so the restart drops every other connection and the device converges on the next supervise pass anyway")
	}
}

/*
TestUpdateInboundRebuildsAnXrayServedInbound is the other arm of the same gate.

It used to assert the opposite — delete+add — and so certified the defect: every
edit closed the listener and dropped every live session on that inbound, for a
change xray-core absorbs as a user diff. Renamed nothing; the invariant flipped.
*/
func TestUpdateInboundRebuildsAnXrayServedInbound(t *testing.T) {
	setupConflictDB(t)

	mgr := runtime.NewManager(runtime.LocalDeps{Cores: testCores(t)})
	fake := &fakeNodeRuntime{updateInboundErr: errors.New("xray refused the update")}
	mgr.SetLocalRuntimeOverride(fake)
	runtime.SetManager(mgr)
	t.Cleanup(func() { runtime.SetManager(nil) })

	seedInboundConflict(t, "vless-polarity", "", 46171, model.VLESS, "",
		`{"clients":[{"email":"vl","id":"3a3d2f6e-6f1a-4b7c-9d2e-1f0a5c8b7d61","enable":true}],"decryption":"none"}`)
	seeded := loadInboundByTag(t, "vless-polarity")
	seedClientTraffic(t, seeded.Id, "vl", true)

	update := *loadInboundByTag(t, "vless-polarity")
	update.Remark = "edited"
	if _, _, err := (&InboundService{}).UpdateInbound(&update); err != nil {
		t.Fatalf("UpdateInbound: %v", err)
	}

	if n := fake.updateInbound.Load(); n != 1 {
		t.Fatalf("in-place push ran %d time(s), want 1: a remark edit must converge, not tear the handler down", n)
	}
	if del, add := fake.delInbound.Load(), fake.addInbound.Load(); del != 0 || add != 0 {
		t.Fatalf("delete+add ran %d/%d, want 0/0: that closes the listener and every session on it, for an edit xray absorbs as a user diff", del, add)
	}
}

/*
The one edit that still has to remove it: disabling the inbound. There is no
in-place convergence for "stop serving this", and leaving the listener up would
keep every current client connected to an inbound the panel shows as off.
*/
func TestDisablingAnXrayInboundRemovesItFromTheCore(t *testing.T) {
	setupConflictDB(t)

	mgr := runtime.NewManager(runtime.LocalDeps{Cores: testCores(t)})
	fake := &fakeNodeRuntime{}
	mgr.SetLocalRuntimeOverride(fake)
	runtime.SetManager(mgr)
	t.Cleanup(func() { runtime.SetManager(nil) })

	seedInboundConflict(t, "vless-off", "", 46172, model.VLESS, "",
		`{"clients":[{"email":"off","id":"3a3d2f6e-6f1a-4b7c-9d2e-1f0a5c8b7d62","enable":true}],"decryption":"none"}`)
	seeded := loadInboundByTag(t, "vless-off")
	seedClientTraffic(t, seeded.Id, "off", true)

	update := *loadInboundByTag(t, "vless-off")
	update.Enable = false
	if _, _, err := (&InboundService{}).UpdateInbound(&update); err != nil {
		t.Fatalf("UpdateInbound: %v", err)
	}

	if del := fake.delInbound.Load(); del != 1 {
		t.Fatalf("del ran %d time(s), want 1: a disabled inbound has to leave the core", del)
	}
	if add := fake.addInbound.Load(); add != 0 {
		t.Fatalf("add ran %d time(s) for a disabled inbound, want 0", add)
	}
}
