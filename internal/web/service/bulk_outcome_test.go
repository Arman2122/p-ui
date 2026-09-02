package service

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
Every bulk operation accounts for every row it was handed.

The results carried a count and, for some, a skipped list; attach and detach
reported one line per inbound, so one failure there hid which clients it
cost; reset-traffic and the group changes returned a count that folded an
unknown email into "fewer than asked". Each response now carries an outcome:
the emails it changed, the ones it left alone with a reason, the ones it
could not change with a reason. Bulk-operations block, phase 1.
*/
func seedBulkClients(t *testing.T, port int, emails ...string) *model.Inbound {
	t.Helper()
	ib := mkInbound(t, port, model.VLESS, clientsSettings(t, []model.Client{}))
	payloads := make([]ClientCreatePayload, 0, len(emails))
	for i, email := range emails {
		payloads = append(payloads, ClientCreatePayload{
			Client:     model.Client{Email: email, ID: "aaaaaaaa-1111-4222-8333-00000000000" + string(rune('0'+i)), SubID: "sub-" + email, Enable: true},
			InboundIds: []int{ib.Id},
		})
	}
	res, _, err := (&ClientService{}).BulkCreate(&InboundService{}, payloads)
	if err != nil || res.Created != len(emails) {
		t.Fatalf("seed clients: created %d of %d, err %v, skipped %+v", res.Created, len(emails), err, res.Skipped)
	}
	return ib
}

func hasRow(rows []BulkRow, email, reasonPart string) bool {
	for _, r := range rows {
		if r.Email == email && strings.Contains(r.Reason, reasonPart) {
			return true
		}
	}
	return false
}

func TestBulkCreateRefusesACredentialAlreadyHeldOnTheInbound(t *testing.T) {
	setupBulkDB(t)
	ib := mkInbound(t, 23911, model.VLESS, clientsSettings(t, []model.Client{}))
	const shared = "bbbbbbbb-1111-4222-8333-777777777777"
	payloads := []ClientCreatePayload{
		{Client: model.Client{Email: "first@x", ID: shared, SubID: "sub-first"}, InboundIds: []int{ib.Id}},
		{Client: model.Client{Email: "second@x", ID: shared, SubID: "sub-second"}, InboundIds: []int{ib.Id}},
	}
	res, _, err := (&ClientService{}).BulkCreate(&InboundService{}, payloads)
	if err != nil {
		t.Fatalf("BulkCreate: %v", err)
	}
	if res.Created != 1 || len(res.Skipped) != 1 {
		t.Fatalf("created=%d skipped=%+v, want the first row created and the second refused", res.Created, res.Skipped)
	}
	if res.Skipped[0].Email != "second@x" || !strings.Contains(res.Skipped[0].Reason, "already used by client first@x") {
		t.Fatalf("skipped = %+v, want second@x refused for first@x's credential", res.Skipped[0])
	}
	if len(res.Outcome.OK) != 1 || res.Outcome.OK[0] != "first@x" || !hasRow(res.Outcome.Skipped, "second@x", "already used") {
		t.Fatalf("outcome = %+v", res.Outcome)
	}
}

func TestBulkAttachAccountsPerClientNotPerInbound(t *testing.T) {
	setupBulkDB(t)
	seedBulkClients(t, 23912, "att-a@x", "att-b@x")
	target := mkInbound(t, 23913, model.VLESS, clientsSettings(t, []model.Client{}))

	res, _, err := (&ClientService{}).BulkAttach(&InboundService{}, []string{"att-a@x", "att-b@x", "ghost@x"}, []int{target.Id, 999999})
	if err != nil {
		t.Fatalf("BulkAttach: %v", err)
	}
	for _, email := range []string{"att-a@x", "att-b@x"} {
		if !contains(res.Outcome.OK, email) {
			t.Errorf("%s attached to the live inbound but is not in ok: %+v", email, res.Outcome)
		}
		if !hasRow(res.Outcome.Failed, email, "inbound 999999") {
			t.Errorf("%s has no failed row naming the missing inbound: %+v", email, res.Outcome.Failed)
		}
	}
	if !hasRow(res.Outcome.Failed, "ghost@x", "") {
		t.Errorf("an unknown email has no failed row: %+v", res.Outcome.Failed)
	}
}

func TestBulkResetAndGroupNameTheEmailsThatMatchedNobody(t *testing.T) {
	setupBulkDB(t)
	seedBulkClients(t, 23914, "rst-a@x")
	svc := &ClientService{}

	reset, err := svc.BulkResetTrafficOutcome(&InboundService{}, []string{"rst-a@x", "nobody@x"})
	if err != nil {
		t.Fatalf("BulkResetTrafficOutcome: %v", err)
	}
	if !contains(reset.Outcome.OK, "rst-a@x") || !hasRow(reset.Outcome.Skipped, "nobody@x", "client not found") {
		t.Fatalf("reset outcome = %+v", reset.Outcome)
	}

	group, err := svc.AddToGroupOutcome([]string{"rst-a@x", "nobody@x"}, "vip")
	if err != nil {
		t.Fatalf("AddToGroupOutcome: %v", err)
	}
	if !contains(group.Outcome.OK, "rst-a@x") || !hasRow(group.Outcome.Skipped, "nobody@x", "client not found") {
		t.Fatalf("group outcome = %+v", group.Outcome)
	}
}

func TestBulkEnableDisableDeleteListWhoTheyChanged(t *testing.T) {
	setupBulkDB(t)
	seedBulkClients(t, 23915, "en-a@x", "en-b@x")
	svc := &ClientService{}
	inboundSvc := &InboundService{}

	off, _, err := svc.BulkSetEnable(inboundSvc, []string{"en-a@x", "nobody@x"}, false)
	if err != nil {
		t.Fatalf("BulkSetEnable: %v", err)
	}
	if !contains(off.Outcome.OK, "en-a@x") || !hasRow(off.Outcome.Skipped, "nobody@x", "") {
		t.Fatalf("disable outcome = %+v", off.Outcome)
	}
	del, _, err := svc.BulkDelete(inboundSvc, []string{"en-b@x", "nobody@x"}, false)
	if err != nil {
		t.Fatalf("BulkDelete: %v", err)
	}
	if !contains(del.Outcome.OK, "en-b@x") || !hasRow(del.Outcome.Skipped, "nobody@x", "") {
		t.Fatalf("delete outcome = %+v", del.Outcome)
	}
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}
