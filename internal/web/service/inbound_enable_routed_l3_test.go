package service

import (
	"fmt"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/runtime"
)

/*
A routed kernel inbound came back dark: disabled, then Xray regenerated for any
reason (its front is not in the config while the inbound is off), then enabled
again — the device's rule pointed at a table holding only the blackhole, and
nothing armed the restart that puts the front back. Every client handshook and
moved nothing until an unrelated restart.
*/
func TestReenablingARoutedKernelInboundRequestsRestart(t *testing.T) {
	setupConflictDB(t)
	mgr := runtime.NewManager(runtime.LocalDeps{Cores: testCores(t)})
	mgr.SetLocalRuntimeOverride(&fakeNodeRuntime{})
	runtime.SetManager(mgr)
	t.Cleanup(func() { runtime.SetManager(nil) })

	seedInboundConflict(t, "wg-home", "", 46170, model.WGKernel, "", `{"clients":[]}`)
	seeded := loadInboundByTag(t, "wg-home")
	db := database.GetDB()
	if err := db.Model(&model.Inbound{}).Where("id = ?", seeded.Id).Update("enable", false).Error; err != nil {
		t.Fatalf("force disable: %v", err)
	}
	unnamed, err := (&InboundService{}).SetInboundEnable(seeded.Id, true)
	if err != nil {
		t.Fatalf("SetInboundEnable: %v", err)
	}
	if unnamed {
		t.Fatal("an inbound no rule names asked for an Xray restart; a kernel inbound outside routing needs none")
	}
	if err := db.Model(&model.Inbound{}).Where("id = ?", seeded.Id).Update("enable", false).Error; err != nil {
		t.Fatalf("force disable: %v", err)
	}
	rule := &model.RoutingRule{Enable: true, IngressScope: model.RoutingScopeSelected, IngressIds: fmt.Sprintf("[%d]", seeded.Id), DestKind: model.RoutingDestDirect, Criteria: "{}"}
	if err := db.Create(rule).Error; err != nil {
		t.Fatalf("seed rule: %v", err)
	}
	needRestart, err := (&InboundService{}).SetInboundEnable(seeded.Id, true)
	if err != nil {
		t.Fatalf("SetInboundEnable: %v", err)
	}
	if !needRestart {
		t.Fatal("re-enabling a routed kernel inbound must request an Xray restart, or its front never comes back and every client is blackholed")
	}
}
