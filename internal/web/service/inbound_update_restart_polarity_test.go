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
