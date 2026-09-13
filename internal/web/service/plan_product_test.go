package service

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/policy"
)

func planInt(n int) *int       { return &n }
func planBytes(n int64) *int64 { return &n }

func createPlanClient(t *testing.T, email string, owner *int) {
	t.Helper()
	db := database.GetDB()
	if err := db.Create(&model.ClientRecord{Email: email, OwnerUserID: owner, Enable: true, TotalGB: 7000, LimitIP: 7}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&core.ClientTraffic{Email: email, Enable: true, Total: 7000, Up: 123, Down: 456}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestPlanProductPersistenceAndScope(t *testing.T) {
	initTestDB(t)
	s := &PolicyService{}
	p, err := s.SavePlan(PlanScope{}, &model.Policy{Name: "family", Description: "A real template", Tiers: `[{"fromBytes":0,"upBps":12,"downBps":34}]`, QuotaBytes: planBytes(9000), DurationDays: planInt(30), LimitIP: planInt(2), StartOnFirstUse: true, AutoRenewDays: 30, ResetCycle: "weekly"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Rev != 1 || p.Description != "A real template" || !p.StartOnFirstUse || p.AutoRenewDays != 30 || p.ResetCycle != "weekly" {
		t.Fatalf("lost metadata: %+v", p)
	}
	copy, err := s.DuplicatePlan(PlanScope{}, p.Id, "family copy")
	if err != nil {
		t.Fatal(err)
	}
	if copy.Id == p.Id || copy.Tiers != p.Tiers || *copy.QuotaBytes != 9000 || copy.Archived {
		t.Fatalf("wrong duplicate: %+v", copy)
	}
	if _, err = s.GetPlan(PlanScope{OwnerUserID: planInt(901)}, p.Id); !errors.Is(err, ErrPolicyUnknown) {
		t.Fatalf("cross-owner read: %v", err)
	}
	createPlanClient(t, "member", nil)
	result, err := s.ApplyPlan(context.Background(), PlanScope{}, "member", p.Id, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.AppliedRev != 1 {
		t.Fatalf("applied revision %d", result.AppliedRev)
	}
	var rec model.ClientRecord
	if err = database.GetDB().Where("email = ?", "member").First(&rec).Error; err != nil {
		t.Fatal(err)
	}
	if rec.TotalGB != 9000 || rec.ExpiryTime != -30*86400000 || rec.LimitIP != 2 || rec.Reset != 30 {
		t.Fatalf("template not applied: %+v", rec)
	}
	var traffic core.ClientTraffic
	if err = database.GetDB().Where("email = ?", "member").First(&traffic).Error; err != nil {
		t.Fatal(err)
	}
	if traffic.Up != 123 || traffic.Down != 456 || traffic.Total != 9000 {
		t.Fatalf("assignment changed usage: %+v", traffic)
	}
	list, err := s.ListPlans(PlanScope{})
	if err != nil {
		t.Fatal(err)
	}
	if list[0].AssignedClients != 1 || list[0].UsedBytes != 579 || list[0].CommittedBytes != 9000 {
		t.Fatalf("wrong stats: %+v", list[0])
	}
	members, err := s.PlanMembers(PlanScope{}, p.Id, 1, 20)
	if err != nil || members.Total != 1 || len(members.Items) != 1 || members.Items[0].Email != "member" || members.Items[0].UsedBytes != 579 {
		t.Fatalf("members: %+v %v", members, err)
	}
	if _, err = s.ArchivePlan(PlanScope{}, p.Id, true); err != nil {
		t.Fatal(err)
	}
	createPlanClient(t, "new-member", nil)
	if _, err = s.ApplyPlan(context.Background(), PlanScope{}, "new-member", p.Id, false); !errors.Is(err, ErrPlanArchived) {
		t.Fatalf("archive allowed assignment: %v", err)
	}
	if _, err = s.ApplyPlan(context.Background(), PlanScope{}, "member", p.Id, false); err != nil {
		t.Fatalf("archive broke existing member: %v", err)
	}
	if err = s.DeletePlan(PlanScope{}, p.Id); err != nil {
		t.Fatal(err)
	}
	var a model.ClientPolicy
	if err = database.GetDB().Where("email = ?", "member").First(&a).Error; err != nil {
		t.Fatal(err)
	}
	if a.PolicyId != nil {
		t.Fatalf("deleted plan still assigned: %+v", a)
	}
}

type planQuotaCore struct {
	*declaredCore
	userCore
	users []core.User
}

func (c *planQuotaCore) ResetQuota(context.Context, string) error { return nil }
func (c *planQuotaCore) AddUser(_ context.Context, _ core.Instance, user core.User) error {
	if c.failWith != nil {
		return c.failWith
	}
	c.users = append(c.users, user)
	return nil
}

func TestPlanProductRuntimeApply(t *testing.T) {
	initTestDB(t)
	base := &declaredCore{id: "mtproto", kind: "mtproto"}
	engine := &planQuotaCore{declaredCore: base, userCore: userCore{base}}
	installCores(t, engine)
	inbound := seedPolicyInbound(t, "mtproto", "plan-native", 49902, "native-member")
	if err := database.GetDB().Model(&model.ClientRecord{}).Where("email = ?", "native-member").Update("sub_id", "native-subscription").Error; err != nil {
		t.Fatal(err)
	}
	inbound.Settings = `{"clients":[{"email":"native-member","enable":true,"id":"11111111-1111-1111-1111-111111111111","subId":"native-subscription"}]}`
	if err := database.GetDB().Model(inbound).Update("settings", inbound.Settings).Error; err != nil {
		t.Fatal(err)
	}
	second := seedPolicyInbound(t, "mtproto", "plan-native-second", 49903)
	seedClientRow(t, "native-member", inbound.Id, 123, 456, 0)
	s := &PolicyService{}
	p, err := s.SavePlan(PlanScope{}, &model.Policy{Name: "native", Tiers: "[]", QuotaBytes: planBytes(5000), DurationDays: planInt(2), LimitIP: planInt(3), InboundIDs: []int{inbound.Id, second.Id}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyPlan(context.Background(), PlanScope{}, "native-member", p.Id, false); err != nil {
		t.Fatal(err)
	}
	if len(engine.users) == 0 {
		t.Fatal("plan never reached runtime")
	}
	user := engine.users[len(engine.users)-1]
	if user.QuotaBytes != 5000 || user.LimitIP != 3 || user.ExpiryUnixMilli <= time.Now().UnixMilli() {
		t.Fatalf("wrong runtime limits: %+v", user)
	}
	var rec model.ClientRecord
	if err = database.GetDB().Where("email = ?", "native-member").First(&rec).Error; err != nil {
		t.Fatal(err)
	}
	ids, err := (&ClientService{}).GetInboundIdsForRecord(rec.Id)
	if err != nil || len(ids) != 2 {
		t.Fatalf("plan inbounds not attached: %v %v", ids, err)
	}
	p.QuotaBytes = planBytes(6000)
	p.DurationDays = planInt(3)
	p, err = s.SavePlan(PlanScope{}, p)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("native core unavailable")
	engine.failWith = failure
	if _, err = s.ApplyPlan(context.Background(), PlanScope{}, "native-member", p.Id, false); !errors.Is(err, failure) {
		t.Fatalf("runtime failure hidden: %v", err)
	}
	var a model.ClientPolicy
	if err = database.GetDB().Where("email = ?", "native-member").First(&a).Error; err != nil {
		t.Fatal(err)
	}
	if a.AppliedRev >= p.Rev {
		t.Fatalf("failed runtime reported applied: %+v", a)
	}
	engine.failWith = nil
	if _, err = s.ApplyPlan(context.Background(), PlanScope{}, "native-member", p.Id, false); err != nil {
		t.Fatal(err)
	}
	if err = database.GetDB().Where("email = ?", "native-member").First(&rec).Error; err != nil {
		t.Fatal(err)
	}
	if rec.ExpiryTime != user.ExpiryUnixMilli+86400000 {
		t.Fatalf("retry extended duration twice: %d versus %d", rec.ExpiryTime, user.ExpiryUnixMilli+86400000)
	}
}

func TestPlanProductReapplyPreservesOverridesAndStart(t *testing.T) {
	initTestDB(t)
	s := &PolicyService{}
	createPlanClient(t, "overridden", nil)
	p, err := s.SavePlan(PlanScope{}, &model.Policy{Name: "month", Tiers: "[]", DurationDays: planInt(30), QuotaBytes: planBytes(10000)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyPlan(context.Background(), PlanScope{}, "overridden", p.Id, false); err != nil {
		t.Fatal(err)
	}
	var before model.ClientRecord
	if err = database.GetDB().Where("email = ?", "overridden").First(&before).Error; err != nil {
		t.Fatal(err)
	}
	if err = database.GetDB().Model(&model.ClientPolicy{}).Where("email = ?", "overridden").Update("override_quota", true).Error; err != nil {
		t.Fatal(err)
	}
	p.DurationDays = planInt(60)
	p.QuotaBytes = planBytes(20000)
	p, err = s.SavePlan(PlanScope{}, p)
	if err != nil {
		t.Fatal(err)
	}
	if p.Rev != 2 {
		t.Fatalf("managed edit did not bump revision: %d", p.Rev)
	}
	out, err := s.ReapplyPlan(context.Background(), PlanScope{}, p.Id, false)
	if err != nil || out.Applied != 1 || len(out.Failures) != 0 {
		t.Fatalf("reapply: %+v %v", out, err)
	}
	var after model.ClientRecord
	if err = database.GetDB().Where("email = ?", "overridden").First(&after).Error; err != nil {
		t.Fatal(err)
	}
	if after.TotalGB != 10000 || after.ExpiryTime != before.ExpiryTime+30*86400000 {
		t.Fatalf("override/start moved: before %d after %+v", before.ExpiryTime, after)
	}
	if _, err = s.ApplyPlan(context.Background(), PlanScope{}, "overridden", p.Id, true); err != nil {
		t.Fatal(err)
	}
	if err = database.GetDB().Where("email = ?", "overridden").First(&after).Error; err != nil {
		t.Fatal(err)
	}
	if after.TotalGB != 20000 {
		t.Fatalf("explicit clear did not inherit quota: %d", after.TotalGB)
	}
}

func TestPlanProductQuotaContinuationConstraints(t *testing.T) {
	initTestDB(t)
	base := &declaredCore{id: "ssh", kind: "ssh"}
	installCores(t, newRateCore(base))
	inbound := seedPolicyInbound(t, "ssh", "plan-ssh", 49901, "continuation")
	seedClientRow(t, "continuation", inbound.Id, 0, 0, 0)
	s := &PolicyService{}
	p, err := s.SavePlan(PlanScope{}, &model.Policy{Name: "throttle", Tiers: "[]", QuotaBytes: planBytes(1000), OnDepletion: "throttle", InboundIDs: []int{inbound.Id}})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RecordPreparedPlanAssignment(PlanScope{}, "continuation", p.Id); err != nil {
		t.Fatal(err)
	}
	if err = database.GetDB().Model(&core.ClientTraffic{}).Where("email = ?", "continuation").Updates(map[string]any{"total": 1000, "up": 1200}).Error; err != nil {
		t.Fatal(err)
	}
	plans, err := Plans(database.GetDB())
	if err != nil {
		t.Fatal(err)
	}
	facts, err := factsFor(database.GetDB(), []string{"continuation"}, plans)
	if err != nil {
		t.Fatal(err)
	}
	if got := policy.Evaluate(facts["continuation"].plan, 1200); got != (policy.Limits{UpBps: 8388608, DownBps: 8388608}) {
		t.Fatalf("depletion did not throttle: %+v", got)
	}
	p.DurationDays = planInt(30)
	p, err = s.SavePlan(PlanScope{}, p)
	if err != nil {
		t.Fatal(err)
	}
	facts, err = factsFor(database.GetDB(), []string{"continuation"}, plans)
	if err != nil {
		t.Fatal(err)
	}
	if got := policy.Evaluate(facts["continuation"].plan, 1200); got != (policy.Limits{UpBps: 8388608, DownBps: 8388608}) {
		t.Fatalf("pending duration edit disabled existing continuation: %+v", got)
	}
	p.Burst = true
	if _, err = s.SavePlan(PlanScope{}, p); !errors.Is(err, ErrPlanUnsupported) {
		t.Fatalf("cosmetic burst accepted: %v", err)
	}
	p.Burst = false
	installCores(t, newProxyCore(&declaredCore{id: "ssh", kind: "ssh"}))
	if _, err = s.SavePlan(PlanScope{}, p); !errors.Is(err, ErrPlanUnsupported) {
		t.Fatalf("unenforced throttle accepted: %v", err)
	}
}

func TestPlanProductScheduledReset(t *testing.T) {
	initTestDB(t)
	s := &PolicyService{}
	createPlanClient(t, "cycle", nil)
	p, err := s.SavePlan(PlanScope{}, &model.Policy{Name: "weekly", Tiers: "[]", ResetCycle: "weekly", QuotaBytes: planBytes(9000)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ApplyPlan(context.Background(), PlanScope{}, "cycle", p.Id, false); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	if err = database.GetDB().Model(&model.ClientPolicy{}).Where("email = ?", "cycle").Update("last_reset_at", now.AddDate(0, 0, -8).UnixMilli()).Error; err != nil {
		t.Fatal(err)
	}
	out, err := s.ConvergePlanCycles(context.Background(), now)
	if err != nil || out.Reset != 1 {
		t.Fatalf("reset: %+v %v", out, err)
	}
	var traffic core.ClientTraffic
	if err = database.GetDB().Where("email = ?", "cycle").First(&traffic).Error; err != nil {
		t.Fatal(err)
	}
	if traffic.Up != 0 || traffic.Down != 0 {
		t.Fatalf("reset kept traffic: %+v", traffic)
	}
	out, err = s.ConvergePlanCycles(context.Background(), now)
	if err != nil || out.Reset != 0 {
		t.Fatalf("duplicate reset: %+v %v", out, err)
	}
}

// The update handlers bind the body INTO the stored row, so a key the caller
// leaves out must keep its value: an API rename must not empty the ladder.
func TestPlanPartialUpdateKeepsTheLadder(t *testing.T) {
	initTestDB(t)
	s := &PolicyService{}
	ladder := `[{"fromBytes":0,"upBps":1000000,"downBps":2000000}]`
	p, err := s.SavePlan(PlanScope{}, &model.Policy{Name: "shaped", Tiers: ladder, QuotaBytes: planBytes(5000)})
	if err != nil {
		t.Fatal(err)
	}
	row, err := s.GetPlan(PlanScope{}, p.Id)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"name":"renamed"}`), row); err != nil {
		t.Fatal(err)
	}
	row.Id = p.Id
	updated, err := s.SavePlan(PlanScope{}, row)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := ParsePlan(ladder)
	got, err := ParsePlan(updated.Tiers)
	if err != nil || updated.Name != "renamed" || !slices.Equal(got.Tiers, want.Tiers) || updated.QuotaBytes == nil || *updated.QuotaBytes != 5000 {
		t.Fatalf("partial update lost stored fields: %+v (%v)", updated, err)
	}
	if updated.Rev != p.Rev {
		t.Fatalf("a rename bumped the managed revision: %d -> %d", p.Rev, updated.Rev)
	}
}

func TestPlanNamesAreUniquePerOwner(t *testing.T) {
	initTestDB(t)
	s := &PolicyService{}
	first, err := s.SavePlan(PlanScope{}, &model.Policy{Name: "Monthly", Tiers: "[]"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SavePlan(PlanScope{}, &model.Policy{Name: "monthly ", Tiers: "[]"}); !errors.Is(err, ErrPolicyNameTaken) {
		t.Fatalf("case variant accepted: %v", err)
	}
	if _, err = s.DuplicatePlan(PlanScope{}, first.Id, "Monthly"); !errors.Is(err, ErrPolicyNameTaken) {
		t.Fatalf("duplicate into a taken name accepted: %v", err)
	}
	owner := &model.User{Username: "plan-owner", Role: "reseller"}
	if err := database.GetDB().Create(owner).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.GetDB().Create(&model.ResellerAccount{UserID: owner.Id, Name: "plan-owner", AllowPlans: true}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err = s.SavePlan(PlanScope{OwnerUserID: &owner.Id}, &model.Policy{Name: "Monthly", Tiers: "[]"}); err != nil {
		t.Fatalf("another owner's scope refused the same name: %v", err)
	}
	first.Description = "renamed onto itself"
	if _, err = s.SavePlan(PlanScope{}, first); err != nil {
		t.Fatalf("editing a plan clashed with its own name: %v", err)
	}
}
