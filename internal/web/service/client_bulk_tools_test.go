package service

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestBulkClearIpsForgetsOnlyTheSelectedClients(t *testing.T) {
	setupBulkDB(t)
	seedBulkClients(t, 23919, "ip-a@x", "ip-b@x")
	db := database.GetDB()
	for _, email := range []string{"ip-a@x", "ip-b@x"} {
		if err := db.Create(&model.InboundClientIps{ClientEmail: email, Ips: `["10.0.0.1"]`}).Error; err != nil {
			t.Fatalf("seed ips for %s: %v", email, err)
		}
	}

	res, err := (&ClientService{}).BulkClearIps([]string{"ip-a@x", "ghost@x"})
	if err != nil {
		t.Fatalf("BulkClearIps: %v", err)
	}
	if res.Cleared != 1 || !contains(res.Outcome.OK, "ip-a@x") || !hasRow(res.Outcome.Skipped, "ghost@x", "client not found") {
		t.Fatalf("cleared=%d outcome=%+v, want ip-a@x cleared and ghost@x skipped", res.Cleared, res.Outcome)
	}

	var rows []model.InboundClientIps
	if err := db.Order("client_email ASC").Find(&rows).Error; err != nil {
		t.Fatalf("ips rows: %v", err)
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r.ClientEmail] = r.Ips
	}
	if got["ip-a@x"] != "" || got["ip-b@x"] != `["10.0.0.1"]` {
		t.Fatalf("ips after clear = %v, want ip-a@x emptied and ip-b@x untouched", got)
	}
}

func TestExportEmailsNarrowsToTheSelection(t *testing.T) {
	setupBulkDB(t)
	ib := seedBulkClients(t, 23920, "ex-a@x", "ex-b@x")

	items, err := (&ClientService{}).ExportEmails([]string{"ex-b@x", "ghost@x"})
	if err != nil {
		t.Fatalf("ExportEmails: %v", err)
	}
	if len(items) != 1 || items[0].Client.Email != "ex-b@x" {
		t.Fatalf("items = %+v, want exactly ex-b@x", items)
	}
	if len(items[0].InboundIds) != 1 || items[0].InboundIds[0] != ib.Id {
		t.Fatalf("inboundIds = %v, want [%d]", items[0].InboundIds, ib.Id)
	}
}
