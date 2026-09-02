package service

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestBulkRotateMintsFreshCredentialsAndSubscriptionIds(t *testing.T) {
	setupBulkDB(t)
	ib := seedBulkClients(t, 23921, "rot-a@x", "rot-b@x")
	seeded, err := (&InboundService{}).GetInbound(ib.Id)
	if err != nil {
		t.Fatalf("reload seeded inbound: %v", err)
	}
	before, err := (&InboundService{}).GetClients(seeded)
	if err != nil {
		t.Fatalf("GetClients before: %v", err)
	}
	if len(before) != 2 {
		t.Fatalf("seeded %d clients, want 2", len(before))
	}
	oldIds := map[string]string{}
	for _, c := range before {
		oldIds[c.Email] = c.ID
	}

	if _, _, err := (&ClientService{}).BulkRotate(&InboundService{}, []string{"rot-a@x"}, false, false); err == nil || !strings.Contains(err.Error(), "nothing to rotate") {
		t.Fatalf("nothing requested err = %v, want a refusal", err)
	}

	res, _, err := (&ClientService{}).BulkRotate(&InboundService{}, []string{"rot-a@x", "rot-b@x", "ghost@x"}, true, true)
	if err != nil {
		t.Fatalf("BulkRotate: %v", err)
	}
	if res.Rotated != 2 || !contains(res.Outcome.OK, "rot-a@x") || !contains(res.Outcome.OK, "rot-b@x") || !hasRow(res.Outcome.Skipped, "ghost@x", "client not found") {
		t.Fatalf("rotated=%d outcome=%+v", res.Rotated, res.Outcome)
	}

	fresh, err := (&InboundService{}).GetInbound(ib.Id)
	if err != nil {
		t.Fatalf("reload inbound: %v", err)
	}
	after, err := (&InboundService{}).GetClients(fresh)
	if err != nil {
		t.Fatalf("GetClients after: %v", err)
	}
	db := database.GetDB()
	seen := map[string]bool{}
	for _, c := range after {
		if c.ID == "" || c.ID == oldIds[c.Email] || seen[c.ID] {
			t.Errorf("%s inbound entry id=%q (was %q): want a fresh, unique uuid", c.Email, c.ID, oldIds[c.Email])
		}
		seen[c.ID] = true
		if c.SubID == "" || strings.HasPrefix(c.SubID, "sub-") {
			t.Errorf("%s inbound entry subId=%q, want a fresh one", c.Email, c.SubID)
		}
		var rec model.ClientRecord
		if err := db.Where("email = ?", c.Email).First(&rec).Error; err != nil {
			t.Fatalf("record %s: %v", c.Email, err)
		}
		if rec.UUID != c.ID || rec.SubID != c.SubID {
			t.Errorf("%s record uuid=%q subId=%q, want the inbound entry's %q %q", c.Email, rec.UUID, rec.SubID, c.ID, c.SubID)
		}
	}
}
