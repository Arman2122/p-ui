package service

import (
	"fmt"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/runtime"
	"github.com/Arman2122/p-ui/internal/xray"
)

// A client re-created under a just-deleted email is a live identity again. If
// the tombstone outlives it, the node merge prunes the new client's link.
func TestRecreatedClientSurvivesNodeMerge(t *testing.T) {
	db := initTrafficTestDB(t)
	svc := &ClientService{}
	inboundSvc := &InboundService{}

	const email = "reborn@x"
	seedNodeRow(t, db, &model.Node{Id: 1, Name: "n1", Address: "127.0.0.1", Port: 2096, ApiToken: "tok", Enable: true})
	createNodeInboundWithClient(t, db, 1, "n1-in", 41501, email)
	nodeSettings := fmt.Sprintf(`{"clients": [{"email": %q, "enable": true}]}`, email)
	syncNodeWithSettings(t, inboundSvc, 1, "n1-in", nodeSettings, xray.ClientTraffic{Email: email, Enable: true})

	var ib model.Inbound
	if err := db.Where("tag = ?", "n1-in").First(&ib).Error; err != nil {
		t.Fatalf("load inbound: %v", err)
	}

	rec := &model.ClientRecord{}
	if err := db.Where("email = ?", email).First(rec).Error; err != nil {
		t.Fatalf("load adopted client record: %v", err)
	}
	t.Cleanup(func() { withdrawClientTombstones(email) })
	if _, err := svc.Delete(inboundSvc, rec.Id, false); err != nil {
		t.Fatalf("delete client: %v", err)
	}

	if _, err := svc.Create(inboundSvc, &ClientCreatePayload{
		Client:     model.Client{Email: email, Enable: true, ID: "44444444-4444-4444-4444-444444444444"},
		InboundIds: []int{ib.Id},
	}); err != nil {
		t.Fatalf("re-create client: %v", err)
	}

	// The create marks the node config-dirty, which parks the client merge; the
	// reconcile clears it a tick later, well inside the 90s tombstone window.
	if err := db.Model(&model.Node{}).Where("id = ?", 1).Update("config_dirty", false).Error; err != nil {
		t.Fatalf("clear config_dirty: %v", err)
	}
	snap := &runtime.TrafficSnapshot{Inbounds: []*model.Inbound{{
		Tag: "n1-in", Protocol: model.VLESS, Settings: nodeSettings,
		ClientStats: []xray.ClientTraffic{{Email: email, Enable: true}},
	}}}
	if _, err := inboundSvc.setRemoteTrafficLocked(1, snap, false); err != nil {
		t.Fatalf("node merge: %v", err)
	}

	var links int64
	if err := db.Model(&model.ClientInbound{}).
		Joins("JOIN clients ON clients.id = client_inbounds.client_id").
		Where("clients.email = ? AND client_inbounds.inbound_id = ?", email, ib.Id).
		Count(&links).Error; err != nil {
		t.Fatalf("count client links: %v", err)
	}
	if links != 1 {
		t.Fatalf("re-created client has %d inbound links after the node merge, want 1: the delete tombstone outlived the email and the merge pruned it", links)
	}
}
