package service

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/runtime"
	"github.com/Arman2122/p-ui/internal/xray"
)

func TestSetRemoteTraffic_MissingDisabledInboundIsNotSwept(t *testing.T) {
	setupConflictDB(t)
	db := database.GetDB()
	node := &model.Node{Name: "disabled-snapshot", Address: "127.0.0.1", Port: 2096, ApiToken: "tok", Enable: true, Status: "online"}
	if err := db.Create(node).Error; err != nil {
		t.Fatal(err)
	}
	disabled := &model.Inbound{
		UserId: 1, NodeID: &node.Id, Tag: "disabled", Enable: false,
		Port: 24443, Protocol: model.VLESS, Settings: `{"clients":[]}`,
	}
	reported := &model.Inbound{
		UserId: 1, NodeID: &node.Id, Tag: "reported", Enable: true,
		Port: 24444, Protocol: model.VLESS, Settings: `{"clients":[]}`,
	}
	if err := db.Create(disabled).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(reported).Error; err != nil {
		t.Fatal(err)
	}
	snap := &runtime.TrafficSnapshot{Inbounds: []*model.Inbound{{
		Tag: reported.Tag, Enable: true,
		Port: reported.Port, Protocol: reported.Protocol, Settings: reported.Settings,
	}}}
	if _, err := (&InboundService{}).setRemoteTrafficLocked(node.Id, snap, false); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&model.Inbound{}).Where("id=?", disabled.Id).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("disabled inbound rows=%d, want 1", count)
	}
}

func TestReconcileNode_SaveGrowingSelectionRearmsSweepGuard(t *testing.T) {
	cases := []struct {
		name        string
		storedTags  []string
		mode        string
		tags        []string
		wantDeleted []int
	}{
		{
			name:        "newly selected tag is imported, not swept",
			storedTags:  []string{"keep"},
			mode:        "selected",
			tags:        []string{"keep", "fresh"},
			wantDeleted: nil,
		},
		{
			name:        "switch to all mode imports before sweeping",
			storedTags:  []string{"keep"},
			mode:        "all",
			wantDeleted: nil,
		},
		{
			name:        "unchanged selection still sweeps a deleted tag",
			storedTags:  []string{"keep", "gone"},
			mode:        "selected",
			tags:        []string{"keep", "gone"},
			wantDeleted: []int{3},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setupConflictDB(t)
			ts, deletedIDs := fakeNodePanel(t, map[string]int{"keep": 1, "fresh": 2, "gone": 3})
			node := reconcileTestNode(t, ts, "grow-node", "selected", tc.storedTags)
			seedInboundConflictNode(t, "keep", "", 443, model.VLESS, `{"network":"tcp"}`, `{"clients":[]}`, &node.Id)

			err := (&NodeService{}).UpdateFromRequest(node.Id, &NodeMutationRequest{
				Name:                node.Name,
				Scheme:              node.Scheme,
				Address:             node.Address,
				Port:                node.Port,
				BasePath:            node.BasePath,
				Enable:              true,
				AllowPrivateAddress: true,
				InboundSyncMode:     tc.mode,
				InboundTags:         tc.tags,
			})
			if err != nil {
				t.Fatalf("UpdateFromRequest: %v", err)
			}
			saved := &model.Node{}
			if err := database.GetDB().First(saved, node.Id).Error; err != nil {
				t.Fatalf("reload node: %v", err)
			}

			svc := InboundService{}
			if err := svc.ReconcileNode(context.Background(), runtime.NewRemote(saved, nil), saved); err != nil {
				t.Fatalf("ReconcileNode: %v", err)
			}
			if got := deletedIDs(); !slices.Equal(got, tc.wantDeleted) {
				t.Fatalf("deleted remote ids = %v, want %v", got, tc.wantDeleted)
			}
		})
	}
}

func TestDirtyNodeSnapshotAccumulatesUsageWithoutReplacingLifecycle(t *testing.T) {
	db := initTrafficTestDB(t)
	createNodeInboundWithClient(t, db, 1, "pending", 41001, "pending-user")
	svc := &InboundService{}
	oldExpiry := time.Now().Add(time.Hour).UnixMilli()
	newExpiry := time.Now().Add(24 * time.Hour).UnixMilli()
	syncNode(t, svc, 1, "pending", xray.ClientTraffic{Email: "pending-user", Enable: true, ExpiryTime: oldExpiry, Total: 1000, Reset: 1, Up: 100, Down: 200})
	if err := db.Model(&xray.ClientTraffic{}).Where("email = ?", "pending-user").Updates(map[string]any{"enable": true, "expiry_time": newExpiry, "total": 2000, "reset": 3, "up": 100, "down": 50}).Error; err != nil {
		t.Fatal(err)
	}
	snap := &runtime.TrafficSnapshot{Inbounds: []*model.Inbound{{Tag: "pending", Enable: true, ClientStats: []xray.ClientTraffic{{Email: "pending-user", Enable: false, ExpiryTime: oldExpiry, Total: 1000, Reset: 1, Up: 150, Down: 230}}}}}
	if _, err := svc.setRemoteTrafficLocked(1, snap, true); err != nil {
		t.Fatal(err)
	}
	got := readTraffic(t, db, "pending-user")
	if !got.Enable || got.ExpiryTime != newExpiry || got.Total != 2000 || got.Reset != 3 || got.Up != 150 || got.Down != 80 {
		t.Fatalf("dirty node replaced lifecycle or lost deltas: %+v", got)
	}
}
