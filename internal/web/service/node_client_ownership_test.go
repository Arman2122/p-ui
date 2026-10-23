package service

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/runtime"
	"github.com/Arman2122/p-ui/internal/xray"
)

func TestNodeSnapshotCannotClaimForeignClient(t *testing.T) {
	db := initTrafficTestDB(t)
	seedNodeRow(t, db, &model.Node{Id: 1, Name: "foreign-claim", Address: "127.0.0.1", Port: 2096, ApiToken: "tok", Enable: true})
	legit := model.Client{Email: "victim@node", ID: "11111111-1111-1111-1111-111111111111", Enable: true}
	master := mkInbound(t, 40001, model.VLESS, clientsSettings(t, []model.Client{legit}))
	if err := (&ClientService{}).SyncInbound(db, master.Id, []model.Client{legit}); err != nil {
		t.Fatal(err)
	}
	before := xray.ClientTraffic{Email: legit.Email, InboundId: master.Id, Enable: true, Up: 7, Down: 11, Total: 1000}
	if err := db.Create(&before).Error; err != nil {
		t.Fatal(err)
	}
	createNodeInbound(t, db, 1, "foreign-inbound", 41001)
	settings := clientsSettings(t, []model.Client{
		{Email: legit.Email, ID: "99999999-9999-9999-9999-999999999999", Enable: false},
		{Email: "own@node", ID: "22222222-2222-2222-2222-222222222222", Enable: true},
	})
	snap := &runtime.TrafficSnapshot{Inbounds: []*model.Inbound{{
		Tag: "foreign-inbound", Settings: settings, ClientStats: []xray.ClientTraffic{
			{Email: legit.Email, Up: 900, Down: 900, Enable: false},
			{Email: "own@node", Up: 1, Down: 2, Enable: true},
		},
	}}}
	if _, err := (&InboundService{}).setRemoteTrafficLocked(1, snap, false); err != nil {
		t.Fatal(err)
	}
	if got := readTraffic(t, db, legit.Email); got.Up != before.Up || got.Down != before.Down || !got.Enable {
		t.Fatalf("foreign traffic changed to up=%d down=%d enable=%v", got.Up, got.Down, got.Enable)
	}
	var count int64
	if err := db.Table("client_inbounds ci").Joins("JOIN clients c ON c.id = ci.client_id").
		Joins("JOIN inbounds ib ON ib.id = ci.inbound_id").
		Where("ib.node_id = ? AND c.email = ?", 1, legit.Email).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("foreign client attached to node %d times", count)
	}
	if got := readTraffic(t, db, "own@node"); got.Up != 0 || got.Down != 0 {
		t.Fatalf("node-owned initial baseline = %d/%d, want 0/0", got.Up, got.Down)
	}
	if snap.Inbounds[0].Settings != settings || len(snap.Inbounds[0].ClientStats) != 2 {
		t.Fatal("ownership filtering mutated caller snapshot")
	}
	syncNodeWithSettings(t, &InboundService{}, 1, "foreign-inbound", settings,
		xray.ClientTraffic{Email: legit.Email, Up: 1000, Down: 1000, Enable: false},
		xray.ClientTraffic{Email: "own@node", Up: 2, Down: 4, Enable: true})
	if got := readTraffic(t, db, "own@node"); got.Up != 1 || got.Down != 2 {
		t.Fatalf("node-owned traffic delta = %d/%d, want 1/2", got.Up, got.Down)
	}
	if got := readTraffic(t, db, legit.Email); got.Up != before.Up || got.Down != before.Down || !got.Enable {
		t.Fatalf("second snapshot changed foreign traffic: %+v", got)
	}
}
