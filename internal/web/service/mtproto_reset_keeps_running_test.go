package service

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/mtproto"
	"github.com/Arman2122/p-ui/internal/web/runtime"
)

/*
Resetting traffic must not stop an MTProto inbound.

resetAllMtprotoQuotas converged before resetting, and it converged with
Manager.Reconcile -- which treats absence from the desired set as "stop this".
It was the only non-core caller of that destructive API in the tree.

The desired set is built by DesiredMtprotoInstances, which SKIPS any inbound
whose settings will not parse. So an inbound with a malformed settings blob --
a hand edit, a partially written import, a field an older build wrote -- is
absent from `desired` while its sidecar is running perfectly well. Resetting
traffic then killed it, and the panel's own reset-and-re-enable path calls this,
so a renewal was capable of taking a working inbound permanently offline.

Ensure converges the instances that ARE desired and stops nothing. It is a
strict behavioural subset of the call it replaces: Reconcile is exactly this
loop plus the stop-the-absent pass. F217.
*/
func TestATrafficResetDoesNotStopAnUnparseableMtprotoInbound(t *testing.T) {
	setupConflictDB(t)
	pidFile := installFakeMtg(t)
	runtime.SetManager(runtime.NewManager(runtime.LocalDeps{Cores: testCores(t)}))
	t.Cleanup(func() { runtime.SetManager(nil) })

	seedInboundConflict(t, "mt-reset", "", 46201, model.MTProto, "",
		`{"clients":[{"email":"mtres","secret":"`+mtprotoTestSecretA+`","enable":true}]}`)
	seeded := loadInboundByTag(t, "mt-reset")
	seedClientTraffic(t, seeded.Id, "mtres", true)

	primed, ok := mtproto.InstanceFromInbound(seeded)
	if !ok {
		t.Fatal("the seeded inbound must produce an mtg instance")
	}
	if err := mtproto.GetManager().Ensure(primed); err != nil {
		t.Fatalf("prime mtg: %v", err)
	}
	t.Cleanup(func() { mtproto.GetManager().Remove(seeded.Id) })
	waitForSpawns(t, pidFile, 1)

	// Now make the STORED settings unreadable, exactly as a hand edit or a
	// half-written import leaves them. The sidecar keeps running on what it was
	// given; only the panel's view of it breaks.
	if err := database.GetDB().Model(&model.Inbound{}).Where("id = ?", seeded.Id).
		Update("settings", `{"clients": [ this is not json`).Error; err != nil {
		t.Fatalf("corrupt the settings: %v", err)
	}
	desired, err := (&InboundService{}).DesiredMtprotoInstances()
	if err != nil {
		t.Fatalf("DesiredMtprotoInstances: %v", err)
	}
	for _, inst := range desired {
		if inst.Id == seeded.Id {
			t.Skip("the unreadable inbound is still desired; this test cannot see the bug")
		}
	}
	if !mtproto.GetManager().HasRunning() {
		t.Fatal("the sidecar is not running before the reset; the rest proves nothing")
	}

	(&InboundService{}).resetAllMtprotoQuotas()

	if !mtproto.GetManager().HasRunning() {
		t.Fatal("a traffic reset stopped a running MTProto sidecar because the panel could not parse that inbound's stored settings; every client on it is disconnected and nothing restarts it")
	}
}
