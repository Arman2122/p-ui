package service

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/xray"
)

func TestBulkSetWritesEveryStoreAndSkipsADelayedStartExpiry(t *testing.T) {
	setupBulkDB(t)
	ib := seedBulkClients(t, 23916, "set-a@x", "set-b@x")
	db := database.GetDB()
	if err := db.Model(&model.ClientRecord{}).Where("email = ?", "set-b@x").Update("expiry_time", -86400000).Error; err != nil {
		t.Fatalf("seed delayed start: %v", err)
	}

	limit, comment, expiry, group := 3, "Q4 batch", int64(1767225600000), "resellers"
	patch := BulkSetPatch{LimitIP: &limit, Comment: &comment, ExpiryTime: &expiry, Group: &group}
	res, _, err := (&ClientService{}).BulkSet(&InboundService{}, []string{"set-a@x", "set-b@x", "ghost@x"}, patch)
	if err != nil {
		t.Fatalf("BulkSet: %v", err)
	}
	if res.Updated != 2 || !contains(res.Outcome.OK, "set-a@x") || !contains(res.Outcome.OK, "set-b@x") {
		t.Fatalf("updated=%d outcome=%+v, want both live clients ok", res.Updated, res.Outcome)
	}
	if !hasRow(res.Outcome.Skipped, "set-b@x", "delayed start") || !hasRow(res.Outcome.Skipped, "ghost@x", "client not found") {
		t.Fatalf("skipped = %+v, want the delayed-start expiry and the unknown email named", res.Outcome.Skipped)
	}

	fresh, err := (&InboundService{}).GetInbound(ib.Id)
	if err != nil {
		t.Fatalf("reload inbound: %v", err)
	}
	clients, err := (&InboundService{}).GetClients(fresh)
	if err != nil {
		t.Fatalf("GetClients: %v", err)
	}
	for _, c := range clients {
		if c.LimitIP != limit || c.Comment != comment {
			t.Errorf("%s inbound entry limitIp=%d comment=%q, want %d %q", c.Email, c.LimitIP, c.Comment, limit, comment)
		}
		wantExpiry := expiry
		if c.Email == "set-b@x" {
			wantExpiry = 0
		}
		if c.ExpiryTime != wantExpiry {
			t.Errorf("%s inbound entry expiry=%d, want %d", c.Email, c.ExpiryTime, wantExpiry)
		}
	}

	var recs []model.ClientRecord
	if err := db.Where("email IN ?", []string{"set-a@x", "set-b@x"}).Find(&recs).Error; err != nil {
		t.Fatalf("records: %v", err)
	}
	for _, r := range recs {
		if r.Group != group || r.Comment != comment || r.LimitIP != limit {
			t.Errorf("%s record group=%q comment=%q limitIp=%d, want %q %q %d", r.Email, r.Group, r.Comment, r.LimitIP, group, comment, limit)
		}
	}
	var traffic xray.ClientTraffic
	if err := db.Where("email = ?", "set-a@x").First(&traffic).Error; err != nil {
		t.Fatalf("traffic row: %v", err)
	}
	if traffic.ExpiryTime != expiry {
		t.Errorf("client_traffics expiry=%d, want %d", traffic.ExpiryTime, expiry)
	}

	if _, _, err := (&ClientService{}).BulkSet(&InboundService{}, []string{"set-a@x"}, BulkSetPatch{}); err == nil || !strings.Contains(err.Error(), "nothing to set") {
		t.Fatalf("empty patch err = %v, want a refusal", err)
	}
}

func TestBulkMoveAttachesToTheTargetBeforeLeavingTheSource(t *testing.T) {
	setupBulkDB(t)
	source := seedBulkClients(t, 23917, "mv-a@x")
	target := mkInbound(t, 23918, model.VLESS, clientsSettings(t, []model.Client{}))

	if _, _, err := (&ClientService{}).BulkMove(&InboundService{}, []string{"mv-a@x"}, []int{source.Id}, []int{source.Id}); err == nil || !strings.Contains(err.Error(), "both source and target") {
		t.Fatalf("same inbound on both sides err = %v, want a refusal", err)
	}

	res, _, err := (&ClientService{}).BulkMove(&InboundService{}, []string{"mv-a@x", "ghost@x"}, []int{source.Id}, []int{target.Id})
	if err != nil {
		t.Fatalf("BulkMove: %v", err)
	}
	if res.Moved != 1 || !contains(res.Outcome.OK, "mv-a@x") {
		t.Fatalf("moved=%d outcome=%+v, want mv-a@x moved", res.Moved, res.Outcome)
	}
	if !hasRow(res.Outcome.Failed, "ghost@x", "attach:") {
		t.Errorf("an unknown email has no attach-stage failed row: %+v", res.Outcome.Failed)
	}

	db := database.GetDB()
	var rec model.ClientRecord
	if err := db.Where("email = ?", "mv-a@x").First(&rec).Error; err != nil {
		t.Fatalf("record: %v", err)
	}
	var links []model.ClientInbound
	if err := db.Where("client_id = ?", rec.Id).Find(&links).Error; err != nil {
		t.Fatalf("links: %v", err)
	}
	if len(links) != 1 || links[0].InboundId != target.Id {
		t.Fatalf("client_inbounds = %+v, want exactly the target inbound %d", links, target.Id)
	}
}
