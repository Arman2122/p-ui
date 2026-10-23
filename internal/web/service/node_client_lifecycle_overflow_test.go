package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/runtime"
	"github.com/Arman2122/p-ui/internal/xray"
)

func TestTrackedNodeDisableUsesOverflowSafeMasterBudget(t *testing.T) {
	db := initTrafficTestDB(t)
	const email = "large-usage"
	const up, down, masterTotal, nodeTotal int64 = 5_000_000_000_000_000_000, 5_000_000_000_000_000_000, 9_000_000_000_000_000_000, 8_000_000_000_000_000_000
	createNodeInboundWithClient(t, db, 1, "usage-owner", 41021, email)
	createNodeInboundWithClient(t, db, 2, "usage-reporter", 41022, email)
	var owner model.Inbound
	if err := db.Where("tag = ?", "usage-owner").First(&owner).Error; err != nil {
		t.Fatal(err)
	}
	expiry := time.Now().Add(time.Hour).UnixMilli()
	master := &xray.ClientTraffic{InboundId: owner.Id, Email: email, Enable: true, ExpiryTime: expiry, Total: masterTotal, Up: up, Down: down, ResetCount: new(int64(0))}
	if err := db.Create(master).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.NodeClientTraffic{NodeId: 2, Email: email, ResetCount: new(int64(0)), ObservedResetCount: new(int64(0))}).Error; err != nil {
		t.Fatal(err)
	}
	settings := fmt.Sprintf(`{"clients":[{"id":"11111111-1111-4111-8111-111111111111","email":%q,"enable":false,"expiryTime":%d,"totalGB":%d}]}`, email, expiry, nodeTotal)
	incoming := xray.ClientTraffic{Email: email, Enable: false, ExpiryTime: expiry, Total: nodeTotal, ResetCount: new(int64(0))}
	snapshot := &runtime.TrafficSnapshot{Inbounds: []*model.Inbound{{Tag: "usage-reporter", Enable: true, Protocol: model.VLESS, Settings: settings, ClientStats: []xray.ClientTraffic{incoming}}}}
	if _, err := (&InboundService{}).setRemoteTrafficLocked(2, snapshot, false); err != nil {
		t.Fatal(err)
	}
	got := readTraffic(t, db, email)
	if got.Enable {
		t.Fatal("overflowed combined usage overrode the node disable despite exhausted master quota")
	}
	if got.Up != up || got.Down != down || got.Total != masterTotal || got.ExpiryTime != expiry || got.ResetCount == nil || *got.ResetCount != 0 {
		t.Fatalf("disable changed master accounting or lifecycle: %+v", got)
	}
}
