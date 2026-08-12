package database

import (
	"database/sql"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
)

// seedLadderOnlyPlan is the shape every install already has: a policy carrying a
// speed ladder and nothing else, with one client assigned to it.
func seedLadderOnlyPlan(t *testing.T, email string) (*model.Policy, *model.ClientRecord, *core.ClientTraffic) {
	t.Helper()
	plan := &model.Policy{Name: "fair use", Tiers: `[{"fromBytes":0,"upBps":1000000,"downBps":2000000}]`}
	if err := db.Create(plan).Error; err != nil {
		t.Fatalf("create plan: %v", err)
	}
	rec := &model.ClientRecord{Email: email, TotalGB: 10 << 30, ExpiryTime: 1735689600000, LimitIP: 2, Enable: true}
	if err := db.Create(rec).Error; err != nil {
		t.Fatalf("create client: %v", err)
	}
	traffic := &core.ClientTraffic{Email: email, Total: 10 << 30, ExpiryTime: 1735689600000, Enable: true}
	if err := db.Create(traffic).Error; err != nil {
		t.Fatalf("create client_traffics: %v", err)
	}
	if err := db.Create(&model.ClientPolicy{Email: email, PolicyId: &plan.Id}).Error; err != nil {
		t.Fatalf("assign plan: %v", err)
	}
	return plan, rec, traffic
}

/*
A plan gains a quota, a term and an IP cap, and the upgrade that adds them must
move nobody's numbers. NULL is what carries that: it means "this plan manages
nothing", so every ladder-only plan behaves on the new binary exactly as it did.
*/
func TestPlanTemplateMigrationIsNoOp(t *testing.T) {
	initTestDB(t)
	plan, rec, traffic := seedLadderOnlyPlan(t, "noop@pui")

	// The upgrade boot: initModels runs against a database that already has rows.
	if err := initModels(); err != nil {
		t.Fatalf("initModels on an already-populated database: %v", err)
	}

	var gotRec model.ClientRecord
	if err := db.First(&gotRec, rec.Id).Error; err != nil {
		t.Fatalf("reload client: %v", err)
	}
	if gotRec.TotalGB != rec.TotalGB || gotRec.ExpiryTime != rec.ExpiryTime || gotRec.LimitIP != rec.LimitIP {
		t.Fatalf("clients moved: got total=%d expiry=%d limitIp=%d want total=%d expiry=%d limitIp=%d",
			gotRec.TotalGB, gotRec.ExpiryTime, gotRec.LimitIP, rec.TotalGB, rec.ExpiryTime, rec.LimitIP)
	}

	var gotTraffic core.ClientTraffic
	if err := db.Where("email = ?", traffic.Email).First(&gotTraffic).Error; err != nil {
		t.Fatalf("reload client_traffics: %v", err)
	}
	if gotTraffic.Total != traffic.Total || gotTraffic.ExpiryTime != traffic.ExpiryTime {
		t.Fatalf("client_traffics moved: got total=%d expiry=%d want total=%d expiry=%d",
			gotTraffic.Total, gotTraffic.ExpiryTime, traffic.Total, traffic.ExpiryTime)
	}

	row := db.Raw(`SELECT quota_bytes, duration_days, limit_ip FROM policies WHERE id = ?`, plan.Id).Row()
	var quota, days, limitIP sql.NullInt64
	if err := row.Scan(&quota, &days, &limitIP); err != nil {
		t.Fatalf("read the new plan columns: %v", err)
	}
	for _, c := range []struct {
		name string
		got  sql.NullInt64
	}{{"quota_bytes", quota}, {"duration_days", days}, {"limit_ip", limitIP}} {
		if c.got.Valid {
			t.Errorf("%s must be NULL on a plan that manages nothing, got %d", c.name, c.got.Int64)
		}
	}
}

/*
A membership naming a deleted inbound carries no evidence anyone needs, unlike an
assignment naming a deleted plan — which is why this FK cascades where that one
sets NULL. Without the constraint the row outlives its inbound and the next plan
edit provisions members onto an inbound that is gone.
*/
func TestPolicyInboundMembershipCascades(t *testing.T) {
	initTestDB(t)

	plan := &model.Policy{Name: "cascade", Tiers: `[]`}
	if err := db.Create(plan).Error; err != nil {
		t.Fatalf("create plan: %v", err)
	}
	inbound := &model.Inbound{UserId: 1, Remark: "cascade-in", Port: 9443, Protocol: model.VLESS, Tag: "cascade-in"}
	if err := db.Create(inbound).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}
	if err := db.Create(&model.PolicyInbound{PolicyId: plan.Id, InboundId: inbound.Id}).Error; err != nil {
		t.Fatalf("create membership: %v", err)
	}

	if err := db.Delete(&model.Inbound{}, inbound.Id).Error; err != nil {
		t.Fatalf("delete inbound: %v", err)
	}
	var left int64
	if err := db.Model(&model.PolicyInbound{}).Where("inbound_id = ?", inbound.Id).Count(&left).Error; err != nil {
		t.Fatalf("count memberships after the inbound went: %v", err)
	}
	if left != 0 {
		t.Fatalf("deleting an inbound must take its plan memberships with it, %d left", left)
	}

	inbound2 := &model.Inbound{UserId: 1, Remark: "cascade-in-2", Port: 9444, Protocol: model.VLESS, Tag: "cascade-in-2"}
	if err := db.Create(inbound2).Error; err != nil {
		t.Fatalf("create second inbound: %v", err)
	}
	if err := db.Create(&model.PolicyInbound{PolicyId: plan.Id, InboundId: inbound2.Id}).Error; err != nil {
		t.Fatalf("create second membership: %v", err)
	}
	if err := db.Delete(&model.Policy{}, plan.Id).Error; err != nil {
		t.Fatalf("delete plan: %v", err)
	}
	if err := db.Model(&model.PolicyInbound{}).Where("policy_id = ?", plan.Id).Count(&left).Error; err != nil {
		t.Fatalf("count memberships after the plan went: %v", err)
	}
	if left != 0 {
		t.Fatalf("deleting a plan must take its memberships with it, %d left", left)
	}
}
