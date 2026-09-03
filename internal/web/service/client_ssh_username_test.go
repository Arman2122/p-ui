package service

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
An SSH client's username is a per-core credential, and those live in
client_credentials rather than as a column: the rows only ever knew MTProto's
two keys, so a saved username came back empty from every read of the clients
table and the client could not log in after its first edit.
*/
func TestSSHUsernameSurvivesTheClientsTable(t *testing.T) {
	setupBulkDB(t)
	svc := &ClientService{}

	source := []model.Client{{Email: "olgsfhx7lk", Username: "uassfrf1", Password: "secret-12", Enable: true}}
	ib := mkInbound(t, 22801, model.SSH, clientsSettings(t, source))
	if err := svc.SyncInbound(nil, ib.Id, source); err != nil {
		t.Fatalf("seed linkage: %v", err)
	}
	listed, err := svc.ListForInbound(nil, ib.Id)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listed) != 1 || listed[0].Username != "uassfrf1" {
		t.Fatalf("the clients table returned %+v; the username was not kept", listed)
	}
	// An edit that omits the username must keep the stored one, as it keeps a secret.
	edited := listed[0]
	edited.Username = ""
	edited.Comment = "renamed"
	if _, err := svc.UpdateByEmail(&InboundService{}, "olgsfhx7lk", edited); err != nil {
		t.Fatalf("update: %v", err)
	}
	again, err := svc.ListForInbound(nil, ib.Id)
	if err != nil {
		t.Fatalf("list again: %v", err)
	}
	if len(again) != 1 || again[0].Username != "uassfrf1" {
		t.Fatalf("after an edit without the field the username is %q", again[0].Username)
	}
}
