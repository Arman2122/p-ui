package service

import (
	"errors"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/xray"

	"gorm.io/gorm"
)

func TestClientHistoryResetAndUnchanged(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()
	rec := model.ClientRecord{Email: "history-client", Enable: true}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatal(err)
	}
	traffic := xray.ClientTraffic{Email: rec.Email, Up: 100, Down: 1000}
	if err := db.Create(&traffic).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Now().Truncate(time.Hour).Add(5 * time.Minute)
	for i, counters := range [][2]int64{{100, 1000}, {400, 1600}, {400, 1600}, {40, 80}, {140, 280}} {
		if err := db.Model(&traffic).Updates(map[string]any{"up": counters[0], "down": counters[1]}).Error; err != nil {
			t.Fatal(err)
		}
		if err := RecordClientHistory(now.Add(time.Duration(i) * 5 * time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	var samples []model.ClientTrafficSample
	if err := db.Find(&samples).Error; err != nil {
		t.Fatal(err)
	}
	if len(samples) != 1 || samples[0].Up != 440 || samples[0].Down != 880 {
		t.Fatalf("hourly reset-safe deltas = %+v, want up=440 down=880 in one bucket", samples)
	}
	var before, after string
	if err := db.Raw("SELECT xmin::text FROM client_history_cursors WHERE email = ?", rec.Email).Scan(&before).Error; err != nil {
		t.Fatal(err)
	}
	if err := RecordClientHistory(now.Add(30 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := db.Raw("SELECT xmin::text FROM client_history_cursors WHERE email = ?", rec.Email).Scan(&after).Error; err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("an unchanged client rewrote its history cursor")
	}
	history, err := readClientHistories(db, []string{rec.Email}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(history[rec.Email]) != 1 || history[rec.Email][0].Up != 440 {
		t.Fatalf("daily history = %+v, want the observed 440 upload bytes", history)
	}
	if err := db.Model(&traffic).Updates(map[string]any{"up": 240, "down": 480}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error {
		if err := CaptureClientHistoryBeforeReset(tx, []string{rec.Email}, now); err != nil {
			return err
		}
		return tx.Model(&traffic).Updates(map[string]any{"up": 0, "down": 0}).Error
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&traffic).Updates(map[string]any{"up": 240, "down": 480}).Error; err != nil {
		t.Fatal(err)
	}
	if err := RecordClientHistory(now.Add(35 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	history, err = readClientHistories(db, []string{rec.Email}, now)
	if err != nil || history[rec.Email][0].Up != 780 || history[rec.Email][0].Down != 1560 {
		t.Fatalf("explicit reset/refill above previous counter = %+v, err=%v", history, err)
	}
	if err := db.Create(&model.ClientGlobalTraffic{MasterGuid: "fresh-master", Email: rec.Email, Up: 10000, Down: 20000, UpdatedAt: time.Now().UnixMilli()}).Error; err != nil {
		t.Fatal(err)
	}
	resetFailure := errors.New("reset refused")
	err = db.Transaction(func(tx *gorm.DB) error {
		if err := CaptureClientHistoryBeforeReset(tx, []string{rec.Email}, now); err != nil {
			return err
		}
		return resetFailure
	})
	if !errors.Is(err, resetFailure) {
		t.Fatalf("failed reset = %v", err)
	}
	if err := RecordClientHistory(now.Add(40 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	history, err = readClientHistories(db, []string{rec.Email}, now)
	if err != nil || history[rec.Email][0].Up != 780 || history[rec.Email][0].Down != 1560 {
		t.Fatalf("failed reset/global replay inflated recorded history: %+v, %v", history, err)
	}
	if err := RecordClientHistory(now.Add(15 * 24 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&model.ClientTrafficSample{}).Count(&count)
	if count != 0 {
		t.Fatalf("expired sample count = %d, want 0", count)
	}
}

func TestClientPagePlanMetadata(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()
	plan := model.Policy{Name: "Monthly", Tiers: "[]", Rev: 2}
	if err := db.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{"assigned", "custom", "missing"} {
		if err := db.Create(&model.ClientRecord{Email: email, Enable: true}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&model.ClientPolicy{Email: "assigned", PolicyId: &plan.Id, AppliedRev: 1, OverrideQuota: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ClientPolicy{Email: "missing"}).Error; err != nil {
		t.Fatal(err)
	}
	page, err := (&ClientService{}).ListPaged(&InboundService{}, nil, ClientPageParams{PageSize: 2, Sort: "email"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].Email != "assigned" || page.Items[1].Email != "custom" {
		t.Fatalf("paged clients = %+v", page.Items)
	}
	metadata := page.Items[0].Plan
	if metadata == nil || metadata.Name != "Monthly" || !metadata.Pending || !metadata.Overridden || metadata.Unresolved {
		t.Fatalf("plan metadata = %+v", metadata)
	}
	if page.Items[1].Plan != nil || len(page.Items[1].History) != 0 {
		t.Fatalf("custom client must not invent plan/history: %+v", page.Items[1])
	}
	plans, err := readClientPlans(db, []string{"missing"})
	if err != nil || plans["missing"] == nil || !plans["missing"].Unresolved {
		t.Fatalf("unresolved assignment = %+v, err=%v", plans, err)
	}
	if err := db.Create(&model.ClientRecord{Email: "cross-master", Enable: true, TotalGB: 150}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&xray.ClientTraffic{Email: "cross-master", Total: 150, Enable: true}).Error; err != nil {
		t.Fatal(err)
	}
	for _, sample := range []model.ClientGlobalTraffic{
		{MasterGuid: "master-up", Email: "cross-master", Up: 100, UpdatedAt: time.Now().UnixMilli()},
		{MasterGuid: "master-down", Email: "cross-master", Down: 100, UpdatedAt: time.Now().UnixMilli()},
	} {
		if err := db.Create(&sample).Error; err != nil {
			t.Fatal(err)
		}
	}
	filtered, err := (&ClientService{}).ListPaged(&InboundService{}, nil, ClientPageParams{Search: "cross-master", Filter: "depleted"})
	if err != nil || filtered.Filtered != 0 {
		t.Fatalf("opposite master directions fabricated depletion: %+v, %v", filtered, err)
	}
	visible, err := (&ClientService{}).ListPaged(&InboundService{}, nil, ClientPageParams{Search: "cross-master"})
	if err != nil || len(visible.Items) != 1 || visible.Items[0].Traffic == nil || visible.Items[0].Traffic.Up+visible.Items[0].Traffic.Down != 100 {
		t.Fatalf("display differs from authoritative combined usage: %+v, %v", visible, err)
	}
	inbound := mkInbound(t, 45891, model.VLESS, `{"clients":[]}`)
	quota := int64(123456)
	if err := db.Model(&plan).Update("quota_bytes", quota).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.PolicyInbound{PolicyId: plan.Id, InboundId: inbound.Id}).Error; err != nil {
		t.Fatal(err)
	}
	payload := ClientCreatePayload{PolicyID: &plan.Id, Client: model.Client{Email: "from-plan", Enable: false, TotalGB: 99}}
	if _, err := (&ClientService{}).Create(&InboundService{}, &payload); err != nil {
		t.Fatal(err)
	}
	var created model.ClientRecord
	if err := db.Where("email = ?", "from-plan").First(&created).Error; err != nil {
		t.Fatal(err)
	}
	if created.TotalGB != quota || created.OwnerUserID != nil {
		t.Fatalf("prepared client = %+v", created)
	}
	var applied model.ClientPolicy
	if err := db.Where("email = ?", created.Email).First(&applied).Error; err != nil {
		t.Fatal(err)
	}
	if applied.PolicyId == nil || *applied.PolicyId != plan.Id || applied.AppliedRev != plan.Rev {
		t.Fatalf("created assignment = %+v", applied)
	}
	if err := db.Model(&plan).Updates(map[string]any{"rev": plan.Rev + 1, "quota_bytes": quota * 2}).Error; err != nil {
		t.Fatal(err)
	}
	currentIDs := []int{inbound.Id}
	edited := *created.ToClient()
	edited.Comment = "Unrelated edit"
	if _, err := (&ClientService{}).UpdateWithPlan(t.Context(), &InboundService{}, created.Email, edited, ClientUpdateOptions{InboundIDs: &currentIDs}); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("email = ?", created.Email).First(&created).Error; err != nil {
		t.Fatal(err)
	}
	if created.TotalGB != quota {
		t.Fatalf("unrelated edit adopted pending plan quota: %d", created.TotalGB)
	}
	override := true
	edited = *created.ToClient()
	edited.TotalGB = 999
	if _, err := (&ClientService{}).UpdateWithPlan(t.Context(), &InboundService{}, created.Email, edited, ClientUpdateOptions{PlanOverrides: &PlanOverrides{Quota: &override}, InboundIDs: &currentIDs}); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("email = ?", created.Email).First(&created).Error; err != nil {
		t.Fatal(err)
	}
	if created.TotalGB != 999 {
		t.Fatalf("explicit quota override = %d, want 999", created.TotalGB)
	}
	sentinel := errors.New("assignment write rejected")
	if err := db.Callback().Create().Before("gorm:create").Register("test:reject-assignment", func(tx *gorm.DB) {
		if tx.Statement.Table == "client_policies" {
			tx.AddError(sentinel)
		}
	}); err != nil {
		t.Fatal(err)
	}
	failed := ClientCreatePayload{PolicyID: &plan.Id, Client: model.Client{Email: "plan-rollback", Enable: true}}
	_, err = (&ClientService{}).Create(&InboundService{}, &failed)
	_ = db.Callback().Create().Remove("test:reject-assignment")
	if !errors.Is(err, sentinel) {
		t.Fatalf("assignment error=%v, want %v", err, sentinel)
	}
	var remaining int64
	if err := db.Model(&model.ClientRecord{}).Where("email = ?", failed.Client.Email).Count(&remaining).Error; err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("failed assignment left %d client rows", remaining)
	}
}

func TestGroupQuotaDisablesOnlyExhaustedMembers(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()
	svc := &ClientService{}
	if err := svc.CreateGroup("pool"); err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{"a", "b", "outside"} {
		group := "pool"
		if email == "outside" {
			group = ""
		}
		if err := db.Create(&model.ClientRecord{Email: email, Enable: true, Group: group}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&xray.ClientTraffic{Email: email, Enable: true, Up: 40, Down: 20}).Error; err != nil {
			t.Fatal(err)
		}
	}
	inbound := mkInbound(t, 45892, model.VLESS, `{"clients":[]}`)
	ids := []int{inbound.Id}
	if err := svc.SetGroupQuota(GroupQuotaRequest{Name: "pool", QuotaBytes: 100, InboundIDs: &ids}); err != nil {
		t.Fatal(err)
	}
	var enabled []string
	if err := db.Model(&model.ClientRecord{}).Where("enable = TRUE").Pluck("email", &enabled).Error; err != nil {
		t.Fatal(err)
	}
	if len(enabled) != 1 || enabled[0] != "outside" {
		t.Fatalf("enabled clients = %v, want only outside", enabled)
	}
	details, err := svc.ListGroupDetails()
	if err != nil || len(details) != 1 || details[0].TrafficUsed != 120 || details[0].QuotaBytes != 100 {
		t.Fatalf("pool usage=%+v, err=%v", details, err)
	}
	if len(details[0].SharedInboundIDs) != 1 || details[0].SharedInboundIDs[0] != inbound.Id {
		t.Fatalf("shared set=%+v", details[0])
	}
	if err := db.Create(&model.ClientRecord{Email: "late-member", Group: "pool", Enable: false}).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.ConvergeGroupMemberships(); err != nil {
		t.Fatal(err)
	}
	var attached int64
	if err := db.Table("client_inbounds ci").Joins("JOIN clients c ON c.id = ci.client_id").Where("c.email = ? AND ci.inbound_id = ?", "late-member", inbound.Id).Count(&attached).Error; err != nil {
		t.Fatal(err)
	}
	if attached != 1 {
		t.Fatalf("late member attachments=%d, want 1", attached)
	}
	if err := svc.ResetGroupTraffic("pool"); err != nil {
		t.Fatal(err)
	}
	details, err = svc.ListGroupDetails()
	if err != nil || details[0].TrafficUsed != 0 {
		t.Fatalf("reset pool=%+v, err=%v", details, err)
	}
	owner := model.User{Username: "group-owner", Password: "unused", Role: "reseller"}
	if err := db.Create(&owner).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ResellerAccount{UserID: owner.Id, Name: "Group owner"}).Error; err != nil {
		t.Fatal(err)
	}
	owned := model.ClientRecord{Email: "owned-member", OwnerUserID: &owner.Id, Enable: false}
	if err := db.Create(&owned).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddToGroup([]string{owned.Email}, "pool"); !errors.Is(err, ErrResellerForbidden) {
		t.Fatalf("owned group assignment = %v", err)
	}
	result, _, err := svc.BulkAttach(&InboundService{}, []string{owned.Email}, ids)
	if err != nil || len(result.Outcome.Failed) != 1 || result.Outcome.Failed[0].Reason != ErrResellerForbidden.Error() {
		t.Fatalf("ungranted bulk attachment = %+v, %v", result, err)
	}
	if err := db.Model(&model.ClientInbound{}).Where("client_id = ?", owned.Id).Count(&attached).Error; err != nil {
		t.Fatal(err)
	}
	if attached != 0 {
		t.Fatalf("owned client inherited %d privileged attachments", attached)
	}
	source := model.Inbound{Tag: "owned-source", Port: 45893, Protocol: model.VLESS, Settings: `{"clients":[]}`, Enable: true}
	if err := db.Create(&source).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ResellerInboundGrant{UserID: owner.Id, InboundID: source.Id}).Error; err != nil {
		t.Fatal(err)
	}
	result, _, err = svc.BulkAttach(&InboundService{}, []string{owned.Email}, []int{source.Id})
	if err != nil || len(result.Errors) > 0 {
		t.Fatalf("granted attachment = %+v, %v", result, err)
	}
	moved, _, err := svc.BulkMove(&InboundService{}, []string{owned.Email}, []int{source.Id}, ids)
	if err != nil || moved.Moved != 0 || len(moved.Outcome.Failed) != 1 {
		t.Fatalf("ungranted move = %+v, %v", moved, err)
	}
	if err := db.Model(&model.ClientInbound{}).Where("client_id = ? AND inbound_id = ?", owned.Id, source.Id).Count(&attached).Error; err != nil {
		t.Fatal(err)
	}
	if attached != 1 {
		t.Fatal("refused bulk move detached the permitted source")
	}
	if err := svc.CreateGroup("destination"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddToGroup([]string{"a"}, "destination"); err != nil {
		t.Fatal(err)
	}
	groups, err := listManagedGroupSummaries()
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range groups {
		if group.TrafficUsed != 0 {
			t.Fatalf("moving membership transferred old bytes: %+v", group)
		}
	}
	if err := db.Model(&xray.ClientTraffic{}).Where("email = ?", "a").Update("up", 50).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RemoveFromGroup([]string{"a"}); err != nil {
		t.Fatal(err)
	}
	groups, err = listManagedGroupSummaries()
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range groups {
		if group.Name == "destination" && group.TrafficUsed != 10 {
			t.Fatalf("leaving group erased its consumed bytes: %+v", group)
		}
	}
}
