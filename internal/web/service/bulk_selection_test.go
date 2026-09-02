package service

import (
	"sort"
	"strings"
	"testing"
)

/*
A bulk operation can act on everything the list's filter matches, not only
the page on screen.

The Clients page selected rows of the current server page and nothing else;
"all clients in group X" meant paging through and repeating the action. A
bulk request may now name the list page's filter instead of an email list,
resolved server-side with the same scoping the page uses, and refused past a
cap rather than silently truncated. Bulk operations, phase 1.
*/
func TestResolveEmailsHonoursTheFilterAndTheCap(t *testing.T) {
	setupBulkDB(t)
	seedBulkClients(t, 23921, "sel-a@x", "sel-b@x", "sel-c@x")
	svc := &ClientService{}
	if _, err := svc.AddToGroup([]string{"sel-a@x", "sel-c@x"}, "vip"); err != nil {
		t.Fatalf("AddToGroup: %v", err)
	}

	got, err := svc.ResolveEmails(&InboundService{}, &SettingService{}, ClientPageParams{Group: "vip"})
	if err != nil {
		t.Fatalf("ResolveEmails: %v", err)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != "sel-a@x,sel-c@x" {
		t.Fatalf("filter group=vip resolved to %v, want the two grouped clients and nobody else", got)
	}

	previous := bulkSelectionCap
	bulkSelectionCap = 1
	t.Cleanup(func() { bulkSelectionCap = previous })
	if _, err := svc.ResolveEmails(&InboundService{}, &SettingService{}, ClientPageParams{Group: "vip"}); err == nil || !strings.Contains(err.Error(), "narrow the filter") {
		t.Fatalf("a selection over the cap: err = %v, want a refusal asking to narrow the filter", err)
	}
}
