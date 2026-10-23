package service

import (
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/runtime"
	"github.com/Arman2122/p-ui/internal/xray"
)

func TestTrafficResetsRetainNodeRenewalEvidence(t *testing.T) {
	resets := []struct {
		name string
		run  func(*InboundService, int, string) error
	}{
		{"email", func(s *InboundService, _ int, email string) error { return s.ResetClientTrafficByEmail(email) }},
		{"client", func(s *InboundService, _ int, email string) error {
			_, err := (&ClientService{}).ResetTrafficByEmail(s, email)
			return err
		}},
		{"bulk", func(s *InboundService, _ int, email string) error {
			_, err := (&ClientService{}).BulkResetTraffic(s, []string{email})
			return err
		}},
		{"inbound clients", func(s *InboundService, id int, _ string) error {
			return (&ClientService{}).ResetAllClientTraffics(s, id)
		}},
		{"all clients", func(s *InboundService, _ int, _ string) error {
			_, err := (&ClientService{}).ResetAllTraffics(s)
			return err
		}},
	}
	for _, reset := range resets {
		t.Run(reset.name, func(t *testing.T) {
			setupBulkDB(t)
			nodeID, _ := setupNodeRuntime(t)
			db := database.GetDB()
			const email = "reset-renewal@node"
			past := time.Now().Add(-time.Hour).UnixMilli()
			client := model.Client{Email: email, ID: "11111111-1111-1111-1111-111111111111", Enable: true, ExpiryTime: past, Reset: 30, TotalGB: 1000}
			ib := mkInbound(t, 47501, model.VLESS, clientsSettings(t, []model.Client{client}))
			if err := db.Model(ib).Update("node_id", nodeID).Error; err != nil {
				t.Fatal(err)
			}
			if err := (&ClientService{}).SyncInbound(db, ib.Id, []model.Client{client}); err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&xray.ClientTraffic{InboundId: ib.Id, Email: email, Enable: true, ExpiryTime: past, Reset: 30, Total: 1000, Up: 100, Down: 200, ResetCount: new(int64(7))}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&model.NodeClientTraffic{NodeId: nodeID, Email: email, Up: 100, Down: 200, ResetCount: new(int64(3)), ObservedResetCount: new(int64(3))}).Error; err != nil {
				t.Fatal(err)
			}
			svc := &InboundService{}
			if err := reset.run(svc, ib.Id, email); err != nil {
				t.Fatal(err)
			}
			var base model.NodeClientTraffic
			if err := db.Where("node_id = ? AND email = ?", nodeID, email).First(&base).Error; err != nil {
				t.Fatalf("reset discarded renewal evidence: %v", err)
			}
			if base.ResetCount == nil || *base.ResetCount != 3 {
				t.Fatalf("reset changed node renewal generation: %v", base.ResetCount)
			}
			if base.ResetAfterCount == nil || *base.ResetAfterCount != 3 {
				t.Fatalf("reset cutoff = %v, want 3", base.ResetAfterCount)
			}
			requireRenewalCount(t, email, 7)
			if err := db.Model(&model.Node{}).Where("id = ?", nodeID).Update("config_dirty", false).Error; err != nil {
				t.Fatal(err)
			}
			client.ExpiryTime = past + 30*86400000
			snap := &runtime.TrafficSnapshot{Inbounds: []*model.Inbound{{
				Tag: ib.Tag, Enable: true, Protocol: model.VLESS, Settings: clientsSettings(t, []model.Client{client}),
				ClientStats: []xray.ClientTraffic{{Email: email, Enable: true, ExpiryTime: client.ExpiryTime, Reset: 30, Total: 1000, ResetCount: new(int64(4))}},
			}}}
			if _, err := svc.setRemoteTrafficLocked(nodeID, snap, false); err != nil {
				t.Fatal(err)
			}
			got := readTraffic(t, db, email)
			if got.ExpiryTime != client.ExpiryTime || !got.Enable || got.Up != 0 || got.Down != 0 {
				t.Fatalf("post-reset renewal = %+v", got)
			}
			requireRenewalCount(t, email, 8)
		})
	}
}
