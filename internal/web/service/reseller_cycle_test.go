package service

import (
	"errors"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestResellerCyclesAndDepletionPolicyPostgres(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()
	svc := &ResellerService{}
	admin := model.User{Username: "cycle-admin", Role: "admin"}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	day := int64(86400000)
	newAccount := func(name string, shape func(a *model.ResellerAccount)) (*model.User, model.ClientRecord) {
		t.Helper()
		u := &model.User{Username: name, Role: "reseller"}
		if err := db.Create(u).Error; err != nil {
			t.Fatal(err)
		}
		a := model.ResellerAccount{UserID: u.Id, Name: name, TrafficLimit: 1000, ExpiresAt: time.Now().UnixMilli() + 60*day}
		shape(&a)
		if err := db.Create(&a).Error; err != nil {
			t.Fatal(err)
		}
		rec := model.ClientRecord{Email: name + "-client", OwnerUserID: &u.Id, Enable: true}
		if err := db.Create(&rec).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&core.ClientTraffic{Email: rec.Email, Enable: true}).Error; err != nil {
			t.Fatal(err)
		}
		return u, rec
	}
	account := func(id int) model.ResellerAccount {
		t.Helper()
		var a model.ResellerAccount
		if err := db.First(&a, "user_id = ?", id).Error; err != nil {
			t.Fatal(err)
		}
		return a
	}
	enabled := func(id int) bool {
		t.Helper()
		var r model.ClientRecord
		if err := db.First(&r, id).Error; err != nil {
			t.Fatal(err)
		}
		return r.Enable
	}
	count := func(kind string, audience int) int64 {
		t.Helper()
		var n int64
		if err := db.Model(&model.ActivityEvent{}).Where("kind = ? AND audience_user_id = ?", "reseller."+kind, audience).Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n
	}
	pass := func() []string {
		t.Helper()
		disabled, err := svc.EnforceBudgets(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return disabled
	}

	t.Run("block policy keeps clients online and refuses provisioning", func(t *testing.T) {
		u, rec := newAccount("soft-owner", func(a *model.ResellerAccount) { a.OnDepletion = "block"; a.TrafficUsed = 1000 })
		ib := mkInbound(t, 43980, model.VLESS, `{"clients":[]}`)
		if err := db.Create(&model.ResellerInboundGrant{UserID: u.Id, InboundID: ib.Id}).Error; err != nil {
			t.Fatal(err)
		}
		if d := pass(); len(d) != 0 || !enabled(rec.Id) {
			t.Fatalf("soft depletion cut clients: disabled=%v enabled=%v", d, enabled(rec.Id))
		}
		if a := account(u.Id); !a.BudgetBlocked {
			t.Fatal("soft depletion did not block the account")
		}
		if got := count("depleted", u.Id); got != 1 {
			t.Fatalf("depleted notices=%d", got)
		}
		_, err := svc.CreateClient(u, ResellerClientInput{Email: "soft-new", TotalGB: 10, ExpiryTime: time.Now().UnixMilli() + day, Enable: true, InboundIDs: []int{ib.Id}})
		if !errors.Is(err, ErrResellerBudget) {
			t.Fatalf("blocked account provisioned a client: %v", err)
		}
		if err := svc.TopUp(&admin, u.Id, 1000, 0, 0); err != nil {
			t.Fatal(err)
		}
		if a := account(u.Id); a.BudgetBlocked || count("restored", u.Id) != 1 {
			t.Fatalf("top-up did not unblock the soft account: %+v", a)
		}
	})
	t.Run("a suspension cuts and an unsuspend under the soft policy brings paused clients back", func(t *testing.T) {
		u, rec := newAccount("soft-suspend", func(a *model.ResellerAccount) { a.OnDepletion = "block"; a.TrafficUsed = 1000 })
		xs := &XrayService{}
		xs.IsNeedRestartAndSetFalse()
		if err := svc.Suspend(&admin, u.Id, true); err != nil {
			t.Fatal(err)
		}
		if enabled(rec.Id) {
			t.Fatal("suspension left the client enabled")
		}
		if !xs.IsNeedRestartAndSetFalse() {
			t.Fatal("suspension did not arm the restart that cuts live sessions")
		}
		if err := svc.Suspend(&admin, u.Id, false); err != nil {
			t.Fatal(err)
		}
		var paused int64
		if err := db.Model(&model.ResellerPausedClient{}).Where("user_id = ?", u.Id).Count(&paused).Error; err != nil {
			t.Fatal(err)
		}
		if a := account(u.Id); !enabled(rec.Id) || paused != 0 || !a.BudgetBlocked {
			t.Fatalf("unsuspend under block: enabled=%v paused=%d blocked=%v", enabled(rec.Id), paused, a.BudgetBlocked)
		}
	})
	t.Run("a due monthly cycle resets usage once and restores paused clients", func(t *testing.T) {
		u, rec := newAccount("cycle-owner", func(a *model.ResellerAccount) {
			a.ResetCycle, a.TrafficUsed, a.BudgetBlocked, a.PeriodStartedAt = "monthly", 1000, true, time.Now().UnixMilli()-40*day
		})
		if err := db.Model(&model.ClientRecord{}).Where("id = ?", rec.Id).Update("enable", false).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.ResellerPausedClient{UserID: u.Id, ClientID: rec.Id}).Error; err != nil {
			t.Fatal(err)
		}
		before := time.Now().UnixMilli()
		if d := pass(); len(d) != 0 {
			t.Fatalf("cycle pass disabled %v", d)
		}
		a := account(u.Id)
		if a.TrafficUsed != 0 || a.PeriodStartedAt < before || a.BudgetBlocked {
			t.Fatalf("cycle reset left %+v", a)
		}
		if !enabled(rec.Id) || count("reset", u.Id) != 1 || count("restored", u.Id) != 1 {
			t.Fatalf("cycle reset outcome: enabled=%v reset=%d restored=%d", enabled(rec.Id), count("reset", u.Id), count("restored", u.Id))
		}
		pass()
		again := account(u.Id)
		if again.PeriodStartedAt != a.PeriodStartedAt || count("reset", u.Id) != 1 {
			t.Fatal("a second pass reset the period again")
		}
		if next := resellerNextReset(&again); next <= before {
			t.Fatalf("next reset %d is not in the future", next)
		}
	})
	t.Run("manual reset opens a new period and needs an administrator", func(t *testing.T) {
		u, rec := newAccount("manual-owner", func(a *model.ResellerAccount) { a.TrafficUsed = 1000 })
		if d := pass(); len(d) != 1 || enabled(rec.Id) {
			t.Fatalf("depletion did not cut: %v", d)
		}
		if err := svc.ResetUsage(u, u.Id); !errors.Is(err, ErrResellerForbidden) {
			t.Fatalf("reseller reset its own usage: %v", err)
		}
		if err := svc.ResetUsage(&admin, u.Id); err != nil {
			t.Fatal(err)
		}
		if a := account(u.Id); a.TrafficUsed != 0 || a.BudgetBlocked || a.PeriodStartedAt == 0 || !enabled(rec.Id) {
			t.Fatalf("manual reset left %+v enabled=%v", a, enabled(rec.Id))
		}
		if count("reset", u.Id) != 1 || count("restored", u.Id) != 1 {
			t.Fatalf("manual reset notices: reset=%d restored=%d", count("reset", u.Id), count("restored", u.Id))
		}
	})
	t.Run("switching a cycle on anchors the period without forgiving usage", func(t *testing.T) {
		u, _ := newAccount("anchor-owner", func(a *model.ResellerAccount) { a.TrafficUsed = 500; a.CreatedAt = time.Now().UnixMilli() - 40*day })
		ib := &model.Inbound{Tag: "anchor-owner-entry", Enable: true, Port: 43981, Protocol: model.VLESS, Settings: `{"clients":[]}`}
		if err := db.Create(ib).Error; err != nil {
			t.Fatal(err)
		}
		in := ResellerInput{Name: "Anchor", Username: "anchor-owner", TrafficLimit: 1000, InboundIDs: []int{ib.Id}, ResetCycle: "monthly"}
		view, err := svc.Save(&admin, u.Id, in)
		if err != nil {
			t.Fatal(err)
		}
		a := account(u.Id)
		if a.TrafficUsed != 500 || a.PeriodStartedAt == 0 || a.OnDepletion != "disable" || view.NextResetAt <= time.Now().UnixMilli() {
			t.Fatalf("activation: %+v next=%d", a, view.NextResetAt)
		}
		pass()
		if again := account(u.Id); again.TrafficUsed != 500 || count("reset", u.Id) != 0 {
			t.Fatal("activation reset the usage it should have kept")
		}
		in.OnDepletion = "sometimes"
		if _, err := svc.Save(&admin, u.Id, in); !errors.Is(err, ErrResellerInvalid) {
			t.Fatalf("unknown policy accepted: %v", err)
		}
	})
}
