package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/runtime"
	"github.com/Arman2122/p-ui/internal/xray"
)

func TestTrackedNodeLifecycleMerge(t *testing.T) {
	now := time.Now().UnixMilli()
	hour := int64(time.Hour / time.Millisecond)
	for _, tc := range []struct {
		name                     string
		masterExpiry, nodeExpiry int64
		masterEnable, nodeEnable bool
		masterTotal, nodeTotal   int64
		generation               *int64
		dirty                    bool
		wantExpiry               int64
		wantEnable               bool
		wantGeneration           int64
	}{
		{"extension survives stale disable", now + 10*hour, now + hour, true, false, 1000, 1000, new(int64(0)), false, now + 10*hour, true, 10},
		{"shortening survives stale future", now + hour, now + 10*hour, true, true, 1000, 1000, new(int64(0)), false, now + hour, true, 10},
		{"unlimited stays unlimited", 0, now + hour, true, true, 1000, 1000, new(int64(0)), false, 0, true, 10},
		{"zero usage renews using per node generation", now - hour, now + hour, false, true, 1000, 1000, new(int64(1)), false, now + hour, true, 11},
		{"topup survives old quota disable", now + hour, now + hour, true, false, 2000, 1000, new(int64(0)), false, now + hour, true, 10},
		{"matching quota disable remains authoritative", now + hour, now + hour, true, false, 1000, 1000, new(int64(0)), false, now + hour, false, 10},
		{"pending edit freezes renewal", now + 10*hour, now + 20*hour, true, true, 2000, 1000, new(int64(1)), true, now + 10*hour, true, 10},
		{"legacy node keeps established merge", now + hour, now + 10*hour, true, true, 1000, 1000, nil, false, now + 10*hour, true, 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := initTrafficTestDB(t)
			createNodeInboundWithClient(t, db, 1, "owner", 41001, "member")
			createNodeInboundWithClient(t, db, 2, "reporter", 41002, "member")
			var owner model.Inbound
			if err := db.Where("tag = ?", "owner").First(&owner).Error; err != nil {
				t.Fatal(err)
			}
			master := &xray.ClientTraffic{InboundId: owner.Id, Email: "member", Enable: tc.masterEnable, ExpiryTime: tc.masterExpiry, Total: tc.masterTotal, Reset: 1, ResetCount: new(int64(10))}
			if err := db.Create(master).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&model.NodeClientTraffic{NodeId: 2, Email: "member", ResetCount: new(int64(0))}).Error; err != nil {
				t.Fatal(err)
			}
			settings := fmt.Sprintf(`{"clients":[{"id":"11111111-1111-4111-8111-111111111111","email":"member","enable":%t,"expiryTime":%d,"totalGB":%d,"reset":1}]}`, tc.nodeEnable, tc.nodeExpiry, tc.nodeTotal)
			stats := xray.ClientTraffic{Email: "member", Enable: tc.nodeEnable, ExpiryTime: tc.nodeExpiry, Total: tc.nodeTotal, Reset: 1, ResetCount: tc.generation}
			snap := &runtime.TrafficSnapshot{Inbounds: []*model.Inbound{{Tag: "reporter", Enable: true, Protocol: model.VLESS, Settings: settings, ClientStats: []xray.ClientTraffic{stats}}}}
			svc := &InboundService{}
			for tick := 0; tick < 2; tick++ {
				if _, err := svc.setRemoteTrafficLocked(2, snap, tc.dirty); err != nil {
					t.Fatal(err)
				}
				got := readTraffic(t, db, "member")
				if got.ExpiryTime != tc.wantExpiry || got.Enable != tc.wantEnable || got.ResetCount == nil || *got.ResetCount != tc.wantGeneration {
					t.Fatalf("tick%d lifecycle=%+v count=%v; want expiry%d enable%t generation%d", tick, got, got.ResetCount, tc.wantExpiry, tc.wantEnable, tc.wantGeneration)
				}
				if tc.generation != nil && got.Total != tc.masterTotal {
					t.Fatalf("master quota changed: %d", got.Total)
				}
			}
		})
	}
}

func TestNodeRenewalEvidenceSurvivesDeferredMerge(t *testing.T) {
	for _, reason := range []string{"frozen", "incoherent"} {
		t.Run(reason, func(t *testing.T) {
			db := initTrafficTestDB(t)
			createNodeInboundWithClient(t, db, 1, "deferred", 41001, "deferred-user")
			var inbound model.Inbound
			if err := db.Where("tag = ?", "deferred").First(&inbound).Error; err != nil {
				t.Fatal(err)
			}
			oldExpiry := time.Now().Add(-time.Hour).UnixMilli()
			newExpiry := time.Now().Add(time.Hour).UnixMilli()
			row := &xray.ClientTraffic{InboundId: inbound.Id, Email: "deferred-user", Enable: false, ExpiryTime: oldExpiry, Total: 1000, Reset: 1, ResetCount: new(int64(0)), Up: 10}
			if err := db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&model.NodeClientTraffic{NodeId: 1, Email: row.Email, Up: 10, ResetCount: new(int64(0)), ObservedResetCount: new(int64(0))}).Error; err != nil {
				t.Fatal(err)
			}
			settings := func(expiry int64, enable bool) string {
				return fmt.Sprintf(`{"clients":[{"email":"deferred-user","enable":%t,"expiryTime":%d,"totalGB":1000,"reset":1}]}`, enable, expiry)
			}
			wireSettings := settings(newExpiry, true)
			if reason == "incoherent" {
				wireSettings = settings(oldExpiry, false)
			}
			snap := &runtime.TrafficSnapshot{Inbounds: []*model.Inbound{{Tag: "deferred", Enable: true, Protocol: model.VLESS, Settings: wireSettings, ClientStats: []xray.ClientTraffic{{Email: row.Email, Enable: true, ExpiryTime: newExpiry, Total: 1000, Reset: 1, ResetCount: new(int64(1)), Up: 100}}}}}
			svc := &InboundService{}
			for range 2 {
				if _, err := svc.setRemoteTrafficLocked(1, snap, reason == "frozen"); err != nil {
					t.Fatal(err)
				}
				got := readTraffic(t, db, row.Email)
				if got.Up != 110 || got.ExpiryTime != oldExpiry || got.ResetCount == nil || *got.ResetCount != 0 {
					t.Fatalf("deferred merge rebilled bytes or consumed renewal: %+v", got)
				}
			}
			snap.Inbounds[0].Settings = settings(newExpiry, true)
			if _, err := svc.setRemoteTrafficLocked(1, snap, false); err != nil {
				t.Fatal(err)
			}
			got := readTraffic(t, db, row.Email)
			if got.Up != 100 || got.ExpiryTime != newExpiry || !got.Enable || got.ResetCount == nil || *got.ResetCount != 1 {
				t.Fatalf("consistent snapshot lost pending renewal: %+v", got)
			}
		})
	}
}

func TestNodeSnapshotRejectsMixedRenewalEpochs(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprint(reverse), func(t *testing.T) {
			db := initTrafficTestDB(t)
			createNodeInboundWithClient(t, db, 1, "epoch-a", 41001, "epoch-user")
			createNodeInboundWithClient(t, db, 1, "epoch-b", 41002, "epoch-user")
			var inbound model.Inbound
			if err := db.Where("tag = ?", "epoch-a").First(&inbound).Error; err != nil {
				t.Fatal(err)
			}
			oldExpiry := time.Now().Add(-time.Hour).UnixMilli()
			newExpiry := time.Now().Add(time.Hour).UnixMilli()
			if err := db.Create(&xray.ClientTraffic{InboundId: inbound.Id, Email: "epoch-user", Enable: false, ExpiryTime: oldExpiry, Total: 1000, Reset: 1, ResetCount: new(int64(0)), Up: 500}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&model.NodeClientTraffic{NodeId: 1, Email: "epoch-user", Up: 500, ResetCount: new(int64(0)), ObservedResetCount: new(int64(0))}).Error; err != nil {
				t.Fatal(err)
			}
			makeInbound := func(tag string, count, up, expiry int64) *model.Inbound {
				return &model.Inbound{Tag: tag, Enable: true, Protocol: model.VLESS, Settings: fmt.Sprintf(`{"clients":[{"email":"epoch-user","enable":true,"expiryTime":%d,"totalGB":1000,"reset":1}]}`, expiry), ClientStats: []xray.ClientTraffic{{Email: "epoch-user", Enable: true, ExpiryTime: expiry, Total: 1000, Reset: 1, ResetCount: new(count), Up: up}}}
			}
			old, newer := makeInbound("epoch-a", 0, 500, oldExpiry), makeInbound("epoch-b", 1, 20, newExpiry)
			snap := &runtime.TrafficSnapshot{Inbounds: []*model.Inbound{old, newer}}
			if reverse {
				snap.Inbounds[0], snap.Inbounds[1] = snap.Inbounds[1], snap.Inbounds[0]
			}
			svc := &InboundService{}
			_, err := svc.setRemoteTrafficLocked(1, snap, false)
			if err == nil || err.Error() != `node 1 reports inconsistent renewal generations for "epoch-user"` {
				t.Fatalf("mixed epoch error=%v", err)
			}
			if got := readTraffic(t, db, "epoch-user"); got.Up != 500 || got.ExpiryTime != oldExpiry || *got.ResetCount != 0 {
				t.Fatalf("rejected snapshot changed traffic: %+v", got)
			}
			snap.Inbounds = []*model.Inbound{makeInbound("epoch-a", 1, 19, newExpiry), makeInbound("epoch-b", 1, 20, newExpiry)}
			if _, err := svc.setRemoteTrafficLocked(1, snap, false); err != nil {
				t.Fatal(err)
			}
			if got := readTraffic(t, db, "epoch-user"); got.Up != 20 || got.ExpiryTime != newExpiry || *got.ResetCount != 1 {
				t.Fatalf("coherent retry did not converge once: %+v", got)
			}
		})
	}
}

func TestDeferredRenewalCannotUndoManualReset(t *testing.T) {
	for _, frozenAfterReset := range []bool{false, true} {
		t.Run(fmt.Sprint(frozenAfterReset), func(t *testing.T) {
			db := initTrafficTestDB(t)
			createNodeInboundWithClient(t, db, 1, "reset-boundary", 41001, "reset-boundary-user")
			var inbound model.Inbound
			if err := db.Where("tag = ?", "reset-boundary").First(&inbound).Error; err != nil {
				t.Fatal(err)
			}
			oldExpiry, newExpiry := time.Now().Add(-time.Hour).UnixMilli(), time.Now().Add(time.Hour).UnixMilli()
			row := &xray.ClientTraffic{InboundId: inbound.Id, Email: "reset-boundary-user", ExpiryTime: oldExpiry, Total: 1000, Reset: 1, ResetCount: new(int64(0)), Up: 10}
			if err := db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&model.NodeClientTraffic{NodeId: 1, Email: row.Email, Up: 10, ResetCount: new(int64(0)), ObservedResetCount: new(int64(0))}).Error; err != nil {
				t.Fatal(err)
			}
			settings := fmt.Sprintf(`{"clients":[{"email":%q,"enable":true,"expiryTime":%d,"totalGB":1000,"reset":1}]}`, row.Email, newExpiry)
			snap := &runtime.TrafficSnapshot{Inbounds: []*model.Inbound{{Tag: inbound.Tag, Enable: true, Protocol: model.VLESS, Settings: settings, ClientStats: []xray.ClientTraffic{{Email: row.Email, Enable: true, ExpiryTime: newExpiry, Total: 1000, Reset: 1, ResetCount: new(int64(1)), Up: 100}}}}}
			svc := &InboundService{}
			if _, err := svc.setRemoteTrafficLocked(1, snap, true); err != nil {
				t.Fatal(err)
			}
			if err := svc.ResetClientTrafficByEmail(row.Email); err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&xray.ClientTraffic{}).Where("email = ?", row.Email).Update("up", 150).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&model.ClientGlobalTraffic{MasterGuid: "other-master", Email: row.Email, Up: 70, UpdatedAt: time.Now().UnixMilli()}).Error; err != nil {
				t.Fatal(err)
			}
			if frozenAfterReset {
				if _, err := svc.setRemoteTrafficLocked(1, snap, true); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := svc.setRemoteTrafficLocked(1, snap, false); err != nil {
				t.Fatal(err)
			}
			got := readTraffic(t, db, row.Email)
			if got.Up != 150 || got.ExpiryTime != newExpiry || got.ResetCount == nil || *got.ResetCount != 1 {
				t.Fatalf("old renewal restored cleared bytes or lost fresh usage: %+v", got)
			}
			var global model.ClientGlobalTraffic
			if err := db.Where("email = ?", row.Email).First(&global).Error; err != nil {
				t.Fatal(err)
			}
			if global.Up != 70 {
				t.Fatalf("fresh global contribution=%d, want 70", global.Up)
			}
		})
	}
}

func TestLegacyNodeUpgradeRecognizesUnobservedRenewal(t *testing.T) {
	for _, overdue := range []bool{false, true} {
		t.Run(fmt.Sprint(overdue), func(t *testing.T) {
			db := initTrafficTestDB(t)
			createNodeInboundWithClient(t, db, 1, "upgrade-node", 41001, "upgrade-user")
			var inbound model.Inbound
			if err := db.Where("tag = ?", "upgrade-node").First(&inbound).Error; err != nil {
				t.Fatal(err)
			}
			expiry := time.Now().Add(time.Hour).UnixMilli()
			if overdue {
				expiry = time.Now().Add(-time.Hour).UnixMilli()
			}
			renewed := time.Now().Add(24 * time.Hour).UnixMilli()
			row := &xray.ClientTraffic{InboundId: inbound.Id, Email: "upgrade-user", Enable: !overdue, ExpiryTime: expiry, Total: 1000, Reset: 1, ResetCount: new(int64(0))}
			if err := db.Create(row).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&model.NodeClientTraffic{NodeId: 1, Email: row.Email}).Error; err != nil {
				t.Fatal(err)
			}
			settings := fmt.Sprintf(`{"clients":[{"email":%q,"enable":true,"expiryTime":%d,"totalGB":1000,"reset":1}]}`, row.Email, renewed)
			snap := &runtime.TrafficSnapshot{Inbounds: []*model.Inbound{{Tag: inbound.Tag, Enable: true, Protocol: model.VLESS, Settings: settings, ClientStats: []xray.ClientTraffic{{Email: row.Email, Enable: true, ExpiryTime: renewed, Total: 1000, Reset: 1, ResetCount: new(int64(0))}}}}}
			if _, err := (&InboundService{}).setRemoteTrafficLocked(1, snap, false); err != nil {
				t.Fatal(err)
			}
			got := readTraffic(t, db, row.Email)
			wantExpiry, wantCount := expiry, int64(0)
			if overdue {
				wantExpiry, wantCount = renewed, 1
			}
			if got.ExpiryTime != wantExpiry || !got.Enable || got.ResetCount == nil || *got.ResetCount != wantCount {
				t.Fatalf("upgrade lifecycle=%+v, want expiry %d generation %d", got, wantExpiry, wantCount)
			}
		})
	}
}
