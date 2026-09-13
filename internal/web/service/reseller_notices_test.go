package service

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/eventbus"
)

func TestResellerNoticesPostgres(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()
	svc := &ResellerService{}
	admin := model.User{Username: "notice-admin", Role: "admin"}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	u := model.User{Username: "notice-reseller", Role: "reseller"}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	day := int64(86400000)
	if err := db.Create(&model.ResellerAccount{UserID: u.Id, Name: "Notice Co", TrafficLimit: 1000, ExpiresAt: time.Now().UnixMilli() + 30*day}).Error; err != nil {
		t.Fatal(err)
	}
	rec := model.ClientRecord{Email: "notice-client", OwnerUserID: &u.Id, Enable: true}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&core.ClientTraffic{Email: rec.Email, Enable: true}).Error; err != nil {
		t.Fatal(err)
	}
	bus := eventbus.New(32)
	published := make(chan eventbus.Event, 32)
	bus.Subscribe("reseller-notice-test", func(e eventbus.Event) { published <- e })
	prev := eventBus
	SetEventBus(bus)
	t.Cleanup(func() { SetEventBus(prev); bus.Stop() })

	count := func(kind string, audience int) int64 {
		t.Helper()
		var n int64
		if err := db.Model(&model.ActivityEvent{}).Where("kind = ? AND audience_user_id = ?", "reseller."+kind, audience).Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n
	}
	expectOnce := func(kind string) {
		t.Helper()
		if global, own := count(kind, 0), count(kind, u.Id); global != 0 || own != 1 {
			t.Fatalf("%s recorded global=%d reseller=%d, want one in the reseller's feed only", kind, global, own)
		}
		select {
		case e := <-published:
			data, ok := e.Data.(ResellerNotice)
			if e.Type != eventbus.EventResellerBudget || e.Source != u.Username || !ok || data.Kind != kind {
				t.Fatalf("published %+v for %s", e, kind)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s never reached the bus", kind)
		}
	}
	pass := func() []string {
		t.Helper()
		disabled, err := svc.EnforceBudgets(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		return disabled
	}
	set := func(column string, value any) {
		t.Helper()
		if err := db.Model(&model.ResellerAccount{}).Where("user_id = ?", u.Id).Update(column, value).Error; err != nil {
			t.Fatal(err)
		}
	}
	account := func() model.ResellerAccount {
		t.Helper()
		var a model.ResellerAccount
		if err := db.First(&a, "user_id = ?", u.Id).Error; err != nil {
			t.Fatal(err)
		}
		return a
	}
	enabled := func() bool {
		t.Helper()
		var r model.ClientRecord
		if err := db.First(&r, rec.Id).Error; err != nil {
			t.Fatal(err)
		}
		return r.Enable
	}

	t.Run("ninety percent warns once", func(t *testing.T) {
		set("traffic_used", 900)
		if d := pass(); len(d) != 0 {
			t.Fatalf("warning pass disabled %v", d)
		}
		pass()
		expectOnce("warning")
		if !enabled() {
			t.Fatal("warning switched the client off")
		}
	})
	t.Run("depletion disconnects and reports once", func(t *testing.T) {
		set("traffic_used", 1000)
		if d := pass(); !slices.Equal(d, []string{rec.Email}) {
			t.Fatalf("disabled=%v", d)
		}
		if enabled() {
			t.Fatal("depleted owner's client stayed enabled")
		}
		if d := pass(); len(d) != 0 {
			t.Fatalf("idempotent pass disabled %v again", d)
		}
		expectOnce("depleted")
		var row model.ActivityEvent
		if err := db.Where("kind = ? AND audience_user_id = ?", "reseller.depleted", u.Id).First(&row).Error; err != nil {
			t.Fatal(err)
		}
		var params map[string]string
		if err := json.Unmarshal([]byte(row.Params), &params); err != nil {
			t.Fatal(err)
		}
		if params["clients"] != "1" || params["username"] != u.Username || params["limit"] != "0.0" {
			t.Fatalf("depleted params=%v", params)
		}
	})
	t.Run("top-up restores once and re-arms the warning", func(t *testing.T) {
		if err := svc.TopUp(&admin, u.Id, 1000, 0, 0); err != nil {
			t.Fatal(err)
		}
		expectOnce("restored")
		if !enabled() {
			t.Fatal("top-up left the client paused")
		}
		if a := account(); a.TrafficWarnedAt != 0 || a.BudgetBlocked {
			t.Fatalf("half-used account keeps stale state: %+v", a)
		}
		set("traffic_used", 1900)
		pass()
		if got := count("warning", u.Id); got != 2 {
			t.Fatalf("re-armed warning count=%d", got)
		}
		if e := <-published; e.Data.(ResellerNotice).Kind != "warning" {
			t.Fatalf("re-armed warning published %+v", e)
		}
	})
	t.Run("term warns once, then expiry disconnects", func(t *testing.T) {
		set("expires_at", time.Now().UnixMilli()+3*day)
		pass()
		pass()
		expectOnce("expiring")
		if err := svc.TopUp(&admin, u.Id, 0, 30, 0); err != nil {
			t.Fatal(err)
		}
		if a := account(); a.ExpiryWarnedAt != 0 {
			t.Fatal("extending the term kept the expiry warning armed")
		}
		set("expires_at", time.Now().UnixMilli()-1000)
		if d := pass(); !slices.Equal(d, []string{rec.Email}) {
			t.Fatalf("expiry pass disabled %v", d)
		}
		expectOnce("expired")
		if enabled() {
			t.Fatal("expired owner's client stayed enabled")
		}
		set("expires_at", time.Now().UnixMilli()+30*day)
		pass()
		if got := count("restored", u.Id); got != 2 || !enabled() {
			t.Fatalf("restore after term extension: notices=%d enabled=%v", got, enabled())
		}
	})
}
