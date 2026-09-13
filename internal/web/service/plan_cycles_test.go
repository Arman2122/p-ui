package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

// countQueries counts every SELECT GORM issues until the test ends.
func countQueries(t *testing.T) *int {
	t.Helper()
	n := new(int)
	db := database.GetDB()
	if err := db.Callback().Query().Before("gorm:query").Register("test:count-queries", func(*gorm.DB) { *n++ }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Query().Remove("test:count-queries") })
	return n
}

// The pass runs every ten seconds under the assignment lock, so its reads must
// not grow with the number of clients on a cycle.
func TestPlanCyclePassReadsInBatches(t *testing.T) {
	initTestDB(t)
	s := &PolicyService{}
	p, err := s.SavePlan(PlanScope{}, &model.Policy{Name: "weekly", Tiers: "[]", ResetCycle: "weekly", QuotaBytes: planBytes(9000)})
	if err != nil {
		t.Fatal(err)
	}
	for i := range 40 {
		email := fmt.Sprintf("cycle-%02d", i)
		createPlanClient(t, email, nil)
		if err := s.RecordPreparedPlanAssignment(PlanScope{}, email, p.Id); err != nil {
			t.Fatal(err)
		}
	}
	queries := countQueries(t)
	out, err := s.ConvergePlanCycles(context.Background(), time.Now())
	if err != nil || out.Reset != 0 || len(out.Notices) != 0 {
		t.Fatalf("a fresh assignment moved: %+v %v", out, err)
	}
	if *queries > 6 {
		t.Fatalf("the cycle pass issued %d queries for 40 clients; it must not scale with them", *queries)
	}
}

// One notice per depletion, re-armed once usage drops under quota. The first reading is a
// master's push, so the cross-panel usage expression and its bound arguments are exercised.
func TestPlanDepletionNotifiesOnceAndRearms(t *testing.T) {
	initTestDB(t)
	installCores(t, newRateCore(&declaredCore{id: "ssh", kind: "ssh"}))
	inbound := seedPolicyInbound(t, "ssh", "plan-notify", 49905, "notified")
	seedClientRow(t, "notified", inbound.Id, 0, 0, 1000)
	db := database.GetDB()
	if err := db.Model(&model.ClientRecord{}).Where("email = ?", "notified").Update("total_gb", 1000).Error; err != nil {
		t.Fatal(err)
	}
	s := &PolicyService{}
	p, err := s.SavePlan(PlanScope{}, &model.Policy{Name: "notify", Tiers: "[]", QuotaBytes: planBytes(1000), OnDepletion: "notify", InboundIDs: []int{inbound.Id}})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RecordPreparedPlanAssignment(PlanScope{}, "notified", p.Id); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := db.Create(&model.ClientGlobalTraffic{MasterGuid: "master", Email: "notified", Up: 1200, UpdatedAt: now.UnixMilli()}).Error; err != nil {
		t.Fatal(err)
	}
	pass := func(label string, want int) PlanCycleResult {
		t.Helper()
		out, err := s.ConvergePlanCycles(context.Background(), now)
		if err != nil || len(out.Notices) != want {
			t.Fatalf("%s: notices=%+v want %d, err=%v", label, out.Notices, want, err)
		}
		return out
	}
	first := pass("pushed usage over quota", 1)
	if n := first.Notices[0]; n.Email != "notified" || n.PolicyID != p.Id || n.PlanName != "notify" || n.UsedBytes != 1200 || n.QuotaBytes != 1000 {
		t.Fatalf("wrong notice: %+v", n)
	}
	pass("still depleted", 0)
	if err := db.Where("email = ?", "notified").Delete(&model.ClientGlobalTraffic{}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&core.ClientTraffic{}).Where("email = ?", "notified").Update("up", 100).Error; err != nil {
		t.Fatal(err)
	}
	pass("back under quota", 0)
	var a model.ClientPolicy
	if err := db.Where("email = ?", "notified").First(&a).Error; err != nil {
		t.Fatal(err)
	}
	if a.DepletionNotifiedAt != 0 {
		t.Fatalf("dropping under quota did not re-arm the notice: %+v", a)
	}
	if err := db.Model(&core.ClientTraffic{}).Where("email = ?", "notified").Update("up", 1500).Error; err != nil {
		t.Fatal(err)
	}
	pass("depleted again", 1)
}
