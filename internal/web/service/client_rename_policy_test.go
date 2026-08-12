package service

import (
	"context"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
TestARenameCarriesThePlanAssignment.

The assignment is keyed by email precisely so it survives the hard-delete and
recreate a node re-sync performs — which is exactly why a rename has to move it
by hand, the way the quota and the tracking row beside it already are. A lost
assignment is silent in every direction: the client runs at full line rate, the
readback is byte-identical to a client who was never on a plan, and the orphaned
row waits to be inherited by whoever next takes that email.
*/
func TestARenameCarriesThePlanAssignment(t *testing.T) {
	setupBulkDB(t)
	svc := &ClientService{}
	inboundSvc := &InboundService{}
	policySvc := &PolicyService{}

	source := []model.Client{{Email: "old@x", ID: "aaaaaaaa-0000-0000-0000-0000000000a1", SubID: "sub-plan", Enable: true}}
	ib := mkInbound(t, 22101, model.VLESS, clientsSettings(t, source))
	if err := svc.SyncInbound(nil, ib.Id, source); err != nil {
		t.Fatalf("seed linkage: %v", err)
	}
	seedPlan(t, "ladder", theLadder, "old@x")
	seedClientRow(t, "old@x", ib.Id, 120<<30, 0, 0)

	before, err := policySvc.EnforcedFor(context.Background(), "old@x")
	if err != nil {
		t.Fatalf("EnforcedFor before the rename: %v", err)
	}
	if before.WantDownBps != 2_000_000 {
		t.Fatalf("before the rename = %+v, want the 2 Mbps tier the ladder puts 120 GiB on", before)
	}

	renamed := source
	renamed[0].Email = "new@x"
	if _, err := svc.UpdateInboundClient(inboundSvc, &model.Inbound{
		Id: ib.Id, Settings: clientsSettings(t, renamed),
	}, "old@x"); err != nil {
		t.Fatalf("UpdateInboundClient: %v", err)
	}

	after, err := policySvc.EnforcedFor(context.Background(), "new@x")
	if err != nil {
		t.Fatalf("EnforcedFor after the rename: %v", err)
	}
	if after.UsedBytes != before.UsedBytes {
		t.Fatalf("usage after the rename = %d, want the %d the quota kept", after.UsedBytes, before.UsedBytes)
	}
	if after.WantDownBps != before.WantDownBps || after.WantUpBps != before.WantUpBps {
		t.Fatalf("limits after the rename = %+v, want the %+v the plan still entitles them to", after, before)
	}
	if after.PolicyId != before.PolicyId {
		t.Fatalf("plan after the rename = %d, want %d", after.PolicyId, before.PolicyId)
	}

	var orphans int64
	if err := database.GetDB().Model(&model.ClientPolicy{}).Where("email = ?", "old@x").
		Count(&orphans).Error; err != nil {
		t.Fatalf("count orphans: %v", err)
	}
	if orphans != 0 {
		t.Fatalf("%d assignment row(s) left on the old email, which the next client to take it would inherit", orphans)
	}
}
