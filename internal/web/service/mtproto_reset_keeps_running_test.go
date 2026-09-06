package service

import (
	"context"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/mtproto"
	"github.com/Arman2122/p-ui/internal/web/runtime"
)

/*
Resetting traffic must not stop an MTProto inbound.

The reset once converged the desired set before resetting, with a call that
treats absence from that set as "stop this" — and an inbound whose stored
settings will not parse (a hand edit, a half-written import) is absent while
its sidecar runs perfectly well, so a renewal took a working inbound offline.

A panel-wide reset now only asks each quota core to clear counters by name and
converges nothing; an unreadable inbound makes it skip that core with a
warning. This pins that nothing on the path stops a sidecar. F217.
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
	if desired, err := (&InboundService{}).DesiredInstances([]core.Kind{core.Kind(model.MTProto)}); err == nil {
		for _, inst := range desired {
			if inst.ID == seeded.Id {
				t.Skip("the unreadable inbound is still desired; this test cannot see the bug")
			}
		}
	}
	if !mtproto.GetManager().HasRunning() {
		t.Fatal("the sidecar is not running before the reset; the rest proves nothing")
	}

	resetCoreQuotasForAll(context.Background())

	if !mtproto.GetManager().HasRunning() {
		t.Fatal("a traffic reset stopped a running MTProto sidecar because the panel could not parse that inbound's stored settings; every client on it is disconnected and nothing restarts it")
	}
}
