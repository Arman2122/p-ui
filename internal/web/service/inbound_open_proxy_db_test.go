package service

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
The refusal has to be on the save path, not only in a helper.

The form always sends auth, so the UI was never the way in. The REST API is,
/inbounds/import is (it bypasses every validate: tag), and so is the form's
raw-JSON editor, whose payload is pruned rather than re-parsed through the
schema. All three land in AddInbound and UpdateInbound, which is why the guard
sits there. F317.
*/
func TestAddInboundRefusesAccountsTheCoreWouldNeverCheck(t *testing.T) {
	initTestDB(t)

	_, _, _, err := (&InboundService{}).AddInbound(&model.Inbound{
		UserId: 1, Enable: true, Remark: "open", Tag: "open-1",
		Listen: "0.0.0.0", Port: 31801, Protocol: model.Mixed,
		Settings: `{"accounts":[{"user":"a","pass":"b"}],"udp":false}`,
	})
	if err == nil {
		t.Fatal("AddInbound stored an open proxy that displays credentials it does not enforce")
	}

	// And the same inbound with auth set is accepted, so the guard refuses the
	// defect rather than the protocol.
	_, _, _, err = (&InboundService{}).AddInbound(&model.Inbound{
		UserId: 1, Enable: true, Remark: "closed", Tag: "closed-1",
		Listen: "0.0.0.0", Port: 31802, Protocol: model.Mixed,
		Settings: `{"auth":"password","accounts":[{"user":"a","pass":"b"}],"udp":false}`,
	})
	if err != nil {
		t.Fatalf("an enforced mixed inbound must still save: %v", err)
	}
}

/*
An edit of an affected inbound forces the fix.

The guard is on UpdateInbound too, so the broken state cannot survive operator
contact: any save of that row — even one changing something else entirely —
refuses until auth is set. That is the whole non-destructive repair path, and
it is why nothing auto-repairs.
*/
func TestUpdateCannotCarryAnInboundThroughInTheBrokenState(t *testing.T) {
	initTestDB(t)

	// Seeded directly, the way an older panel or the import path stored it.
	row := &model.Inbound{
		UserId: 1, Enable: true, Remark: "legacy", Tag: "legacy-1",
		Listen: "0.0.0.0", Port: 31803, Protocol: model.Mixed,
		Settings: `{"accounts":[{"user":"a","pass":"b"}]}`,
	}
	if err := database.GetDB().Create(row).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	row.Remark = "renamed, nothing to do with auth"
	if _, _, _, err := (&InboundService{}).UpdateInbound(row); err == nil {
		t.Fatal("an edit carried the broken state through: the state must not survive operator contact")
	}

	row.Settings = `{"auth":"password","accounts":[{"user":"a","pass":"b"}]}`
	if _, _, _, err := (&InboundService{}).UpdateInbound(row); err != nil {
		t.Fatalf("the repair must be accepted: %v", err)
	}
}

/*
And a row already stored this way is named at boot, every boot.

The save-time guard protects new writes only. A row already in this state is an
open proxy running right now, and a changelog line does not reach a deployed
box. Detection and disclosure; no repair.
*/
func TestTheBootScanNamesAlreadyStoredOpenProxies(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()

	open := &model.Inbound{
		UserId: 1, Enable: true, Remark: "already open", Tag: "open-2",
		Listen: "0.0.0.0", Port: 31804, Protocol: model.Mixed,
		Settings: `{"accounts":[{"user":"a","pass":"b"}]}`,
	}
	enforced := &model.Inbound{
		UserId: 1, Enable: true, Remark: "fine", Tag: "fine-2",
		Listen: "0.0.0.0", Port: 31805, Protocol: model.Mixed,
		Settings: `{"auth":"password","accounts":[{"user":"a","pass":"b"}]}`,
	}
	bridge := &model.Inbound{
		UserId: 1, Enable: true, Remark: "deliberate", Tag: "bridge-2",
		Listen: "127.0.0.1", Port: 31806, Protocol: model.Mixed,
		Settings: `{"auth":"noauth","udp":false}`,
	}
	for _, row := range []*model.Inbound{open, enforced, bridge} {
		if err := db.Create(row).Error; err != nil {
			t.Fatalf("seed %s: %v", row.Tag, err)
		}
	}

	found := (&InboundService{}).OpenProxyInbounds()
	if len(found) != 1 {
		t.Fatalf("the scan must name exactly the open one, got %d: %v", len(found), tagsOf(found))
	}
	if found[0].Id != open.Id {
		t.Fatalf("the scan named the wrong inbound: %v", tagsOf(found))
	}
}

func tagsOf(inbounds []*model.Inbound) []string {
	out := make([]string, 0, len(inbounds))
	for _, inbound := range inbounds {
		out = append(out, inbound.Tag)
	}
	return out
}
