package job

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/eventbus"
	"github.com/Arman2122/p-ui/internal/presence"
	"github.com/Arman2122/p-ui/internal/web/service"
)

func TestProductMaintenanceIntegration(t *testing.T) {
	initTestDB(t)
	// The traffic job writes shared presence independently of this fixture's database.
	t.Cleanup(func() { presence.Shared().RefreshLocal(nil, nil, time.Now().UnixMilli(), 0) })
	t.Setenv("PUI_LOG_FOLDER", t.TempDir())
	t.Setenv("PUI_IP_LIMIT_BANS", "false")
	installSessionCore(t, &fakeCore{id: "vless"})
	db := database.GetDB()

	t.Run("plan notices persist once with and without bus", func(t *testing.T) {
		user := model.User{Username: "plan-notice-owner", Role: "reseller"}
		if err := db.Create(&user).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.ResellerAccount{UserID: user.Id}).Error; err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			email string
			owner *int
			bus   bool
		}{
			{"owned-plan-notice", &user.Id, true},
			{"admin-plan-notice", nil, false},
		} {
			t.Run(tc.email, func(t *testing.T) {
				var got chan eventbus.Event
				if tc.bus {
					bus := eventbus.New(8)
					t.Cleanup(bus.Stop)
					previous := EventBus
					EventBus = bus
					t.Cleanup(func() { EventBus = previous })
					got = make(chan eventbus.Event, 8)
					bus.Subscribe("plan-test", func(e eventbus.Event) { got <- e })
				}
				p := model.Policy{Name: "Family " + tc.email, Rev: 1, OnDepletion: "notify", OwnerUserID: tc.owner}
				if err := db.Create(&p).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&model.ClientRecord{Email: tc.email, Enable: true, TotalGB: 100, OwnerUserID: tc.owner}).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&core.ClientTraffic{Email: tc.email, Enable: true, Up: 100, Total: 100}).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&model.ClientPolicy{Email: tc.email, PolicyId: &p.Id, AppliedRev: 1, AppliedOnDepletion: "notify"}).Error; err != nil {
					t.Fatal(err)
				}
				job := NewCorePolicyJob()
				job.convergePlans(context.Background(), time.Now())
				job.convergePlans(context.Background(), time.Now())
				var events []model.ActivityEvent
				if err := db.Where("kind = ? AND params->>'email' = ?", "client.plan_depleted", tc.email).Find(&events).Error; err != nil {
					t.Fatal(err)
				}
				audience := 0
				if tc.owner != nil {
					audience = *tc.owner
				}
				if len(events) != 1 || events[0].AudienceUserID != audience || events[0].TitleKey != "events.planDepleted.title" || events[0].MessageKey != "events.planDepleted.body" {
					t.Fatalf("durable plan notice=%+v, want one event for audience %d", events, audience)
				}
				var params map[string]string
				if err := json.Unmarshal([]byte(events[0].Params), &params); err != nil {
					t.Fatal(err)
				}
				if params["plan"] != p.Name || params["email"] != tc.email {
					t.Fatalf("notice params=%v", params)
				}
				if tc.bus {
					e := expectEvent(t, got, "client.plan_depleted", tc.email)
					notice, ok := e.Data.(service.PlanDepletionNotice)
					if !ok || notice.PlanName != p.Name || notice.QuotaBytes != 100 {
						t.Fatalf("bus payload=%+v", e.Data)
					}
					expectQuiet(t, got)
				}
			})
		}
	})

	t.Run("traffic pass persists automatic disable", func(t *testing.T) {
		seedObservedClient(t, "vless", "automatic-disable", 48100, "auto-disabled")
		if err := db.Create(&core.ClientTraffic{Email: "auto-disabled", Enable: true, Up: 100, Total: 100}).Error; err != nil {
			t.Fatal(err)
		}
		job := NewXrayTrafficJob()
		job.Run()
		job.Run()
		var events []model.ActivityEvent
		if err := db.Where("kind = ?", "client.auto_disabled").Find(&events).Error; err != nil {
			t.Fatal(err)
		}
		if len(events) != 1 || events[0].AudienceUserID != 0 || events[0].MessageKey != "events.clientsDisabled.body" {
			t.Fatalf("disabled activity=%+v", events)
		}
		var params map[string]string
		if err := json.Unmarshal([]byte(events[0].Params), &params); err != nil {
			t.Fatal(err)
		}
		if params["count"] != "1" || params["clients"] != "auto-disabled" {
			t.Fatalf("disabled params=%v", params)
		}
	})

	t.Run("generic core pass enforces owners without xray", func(t *testing.T) {
		user := model.User{Username: "budget-job-owner", Role: "reseller"}
		if err := db.Create(&user).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.ResellerAccount{UserID: user.Id, TrafficLimit: 100, TrafficUsed: 100}).Error; err != nil {
			t.Fatal(err)
		}
		c := model.ClientRecord{Email: "budget-job-client", OwnerUserID: &user.Id, Enable: true}
		if err := db.Create(&c).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&core.ClientTraffic{Email: c.Email, Enable: true}).Error; err != nil {
			t.Fatal(err)
		}
		NewCorePolicyJob().Run()
		if err := db.First(&c, c.Id).Error; err != nil {
			t.Fatal(err)
		}
		if c.Enable {
			t.Fatal("core policy pass left a depleted reseller's client enabled")
		}
	})

	t.Run("traffic flush enforces fresh group and owner usage", func(t *testing.T) {
		user := model.User{Username: "flush-budget-owner", Role: "reseller"}
		if err := db.Create(&user).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.ResellerAccount{UserID: user.Id, TrafficLimit: 100}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.ClientGroup{Name: "flush-pool"}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.GroupQuota{Name: "flush-pool", QuotaBytes: 100}).Error; err != nil {
			t.Fatal(err)
		}
		for _, c := range []model.ClientRecord{
			{Email: "flush-owner-client", OwnerUserID: &user.Id, Enable: true},
			{Email: "flush-group-client", Group: "flush-pool", Enable: true},
		} {
			if err := db.Create(&c).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&core.ClientTraffic{Email: c.Email, Enable: true}).Error; err != nil {
				t.Fatal(err)
			}
		}
		previous := Cores
		Cores = registryOf(t, &fakeCore{id: "vless", users: []core.TrafficDelta{
			{Email: "flush-owner-client", Up: 150}, {Email: "flush-group-client", Down: 150},
		}})
		t.Cleanup(func() { Cores = previous })
		NewXrayTrafficJob().Run()
		var enabled int64
		if err := db.Model(&model.ClientRecord{}).Where("email IN ? AND enable", []string{"flush-owner-client", "flush-group-client"}).Count(&enabled).Error; err != nil {
			t.Fatal(err)
		}
		if enabled != 0 {
			t.Fatalf("%d exhausted clients survived the traffic flush", enabled)
		}
		var account model.ResellerAccount
		if err := db.First(&account, "user_id = ?", user.Id).Error; err != nil {
			t.Fatal(err)
		}
		if account.TrafficUsed != 150 {
			t.Fatalf("owner usage=%d, want exactly 150 from SQL accounting", account.TrafficUsed)
		}
	})
}
