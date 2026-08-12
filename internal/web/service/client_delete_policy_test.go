package service

import (
	"context"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
TestDeletingAClientReleasesItsPlan.

The assignment is email-keyed so it survives the hard-delete-and-recreate a node
re-sync performs, and its own documentation says it has a QUOTA's lifetime. The
quota is client_traffics, which every delete path drops — so an assignment that
outlives it is not surviving a re-sync, it is waiting for the next client to take
that email. That client is then throttled on a stranger's ladder with nothing on
any screen saying where it came from.
*/
func TestDeletingAClientReleasesItsPlan(t *testing.T) {
	setupBulkDB(t)
	svc := &ClientService{}
	inboundSvc := &InboundService{}
	policySvc := &PolicyService{}

	source := []model.Client{{Email: "leaver@x", ID: "aaaaaaaa-0000-0000-0000-0000000000d1", SubID: "sub-del", Enable: true}}
	ib := mkInbound(t, 22201, model.VLESS, clientsSettings(t, source))
	if err := svc.SyncInbound(nil, ib.Id, source); err != nil {
		t.Fatalf("seed linkage: %v", err)
	}
	seedPlan(t, "leaver-ladder", theLadder, "leaver@x")
	seedClientRow(t, "leaver@x", ib.Id, 120<<30, 0, 0)

	if _, err := svc.DelInboundClientByEmail(inboundSvc, ib.Id, "leaver@x", false, true); err != nil {
		t.Fatalf("DelInboundClientByEmail: %v", err)
	}

	var left int64
	if err := database.GetDB().Model(&model.ClientPolicy{}).Where("email = ?", "leaver@x").
		Count(&left).Error; err != nil {
		t.Fatalf("count assignments: %v", err)
	}
	if left != 0 {
		t.Fatalf("%d assignment row(s) outlived the client, and the quota beside them is gone", left)
	}

	// The consequence, not just the row: a new client on the recycled email must
	// start on no plan at all.
	reborn := []model.Client{{Email: "leaver@x", ID: "aaaaaaaa-0000-0000-0000-0000000000d2", SubID: "sub-new", Enable: true}}
	nib := mkInbound(t, 22202, model.VLESS, clientsSettings(t, reborn))
	if err := svc.SyncInbound(nil, nib.Id, reborn); err != nil {
		t.Fatalf("seed the recycled email: %v", err)
	}
	seedClientRow(t, "leaver@x", nib.Id, 120<<30, 0, 0)

	limits, err := policySvc.EnforcedFor(context.Background(), "leaver@x")
	if err != nil {
		t.Fatalf("EnforcedFor: %v", err)
	}
	if limits.PolicyId != 0 || limits.WantDownBps != 0 || limits.WantUpBps != 0 {
		t.Fatalf("a new client on a recycled email inherited %+v, want no plan and no rate", limits)
	}
}

/*
TestResettingTrafficKeepsThePlan is the boundary of that release.

A reset zeroes the counters of a client who is renewing, so it moves them back to
the first tier — which is the ladder working, not the ladder ending. Releasing the
assignment here would take a paying customer off their plan every renewal.
*/
func TestResettingTrafficKeepsThePlan(t *testing.T) {
	setupBulkDB(t)
	svc := &ClientService{}
	inboundSvc := &InboundService{}
	policySvc := &PolicyService{}

	source := []model.Client{{Email: "renewer@x", ID: "aaaaaaaa-0000-0000-0000-0000000000d3", SubID: "sub-renew", Enable: true}}
	ib := mkInbound(t, 22203, model.VLESS, clientsSettings(t, source))
	if err := svc.SyncInbound(nil, ib.Id, source); err != nil {
		t.Fatalf("seed linkage: %v", err)
	}
	id := seedPlan(t, "renewer-ladder", theLadder, "renewer@x")
	seedClientRow(t, "renewer@x", ib.Id, 120<<30, 0, 0)

	if err := inboundSvc.ResetClientTrafficByEmail("renewer@x"); err != nil {
		t.Fatalf("ResetClientTrafficByEmail: %v", err)
	}
	after, err := policySvc.EnforcedFor(context.Background(), "renewer@x")
	if err != nil {
		t.Fatalf("EnforcedFor: %v", err)
	}
	if after.PolicyId != id {
		t.Fatalf("plan after a reset = %d, want %d: a renewal is not a departure", after.PolicyId, id)
	}
	if after.UsedBytes != 0 || after.WantDownBps != 0 {
		t.Fatalf("after the reset = %+v, want zero usage back on the ladder's unlimited first tier", after)
	}
}
