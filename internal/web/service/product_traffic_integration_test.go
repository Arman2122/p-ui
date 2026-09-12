package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/runtime"

	"gorm.io/gorm"
)

type productResetRuntime struct {
	fakeNodeRuntime
	err error
}

func (r *productResetRuntime) ResetClientTraffic(context.Context, *model.Inbound, string) error {
	return r.err
}

func TestProductTrafficIntegration(t *testing.T) {
	initTestDB(t)
	t.Setenv("PUI_LOG_FOLDER", t.TempDir())
	db := database.GetDB()
	inbounds, clients := &InboundService{}, &ClientService{}

	t.Run("plan reset retries failed remote propagation", func(t *testing.T) {
		nodeID, _ := setupNodeRuntime(t)
		unavailable := errors.New("reset node unavailable")
		remote := &productResetRuntime{err: unavailable}
		runtime.GetManager().SetRuntimeOverride(nodeID, remote)
		c := model.Client{Email: "remote-cycle-reset", ID: "11111111-1111-1111-1111-111111111111", Enable: true}
		ib := nodeInbound(t, nodeID, 48200, []model.Client{c})
		if err := db.Create(&core.ClientTraffic{Email: c.Email, InboundId: ib.Id, Enable: true, Up: 100}).Error; err != nil {
			t.Fatal(err)
		}
		p := model.Policy{Name: "remote weekly reset", Rev: 1, ResetCycle: "weekly"}
		if err := db.Create(&p).Error; err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		a := model.ClientPolicy{Email: c.Email, PolicyId: &p.Id, AppliedRev: 1, AppliedResetCycle: "weekly", UpdatedAt: now.Add(-8 * 24 * time.Hour).UnixMilli()}
		if err := db.Create(&a).Error; err != nil {
			t.Fatal(err)
		}
		result, err := (&PolicyService{}).ConvergePlanCycles(context.Background(), now)
		if !errors.Is(err, unavailable) || result.Reset != 0 {
			t.Fatalf("failed remote reset reported result=%+v err=%v", result, err)
		}
		if err := db.Where("email = ?", c.Email).First(&a).Error; err != nil {
			t.Fatal(err)
		}
		var node model.Node
		if err := db.First(&node, nodeID).Error; err != nil {
			t.Fatal(err)
		}
		if a.LastResetAt != 0 || !node.ConfigDirty {
			t.Fatalf("failed reset advanced cycle or lost retry: reset=%d dirty=%v", a.LastResetAt, node.ConfigDirty)
		}
		remote.err = nil
		result, err = (&PolicyService{}).ConvergePlanCycles(context.Background(), now)
		if err != nil || result.Reset != 1 {
			t.Fatalf("retry result=%+v err=%v", result, err)
		}
		if err := db.Where("email = ?", c.Email).First(&a).Error; err != nil {
			t.Fatal(err)
		}
		if a.LastResetAt != now.UnixMilli() {
			t.Fatalf("successful retry reset=%d, want %d", a.LastResetAt, now.UnixMilli())
		}
	})

	t.Run("history survives every reset and refill", func(t *testing.T) {
		resets := []struct {
			name string
			run  func(*model.Inbound, string) error
		}{
			{"single email", func(_ *model.Inbound, email string) error { return inbounds.ResetClientTrafficByEmail(email) }},
			{"single inbound", func(ib *model.Inbound, email string) error {
				_, err := inbounds.ResetClientTraffic(ib.Id, email)
				return err
			}},
			{"client entry", func(_ *model.Inbound, email string) error {
				_, err := clients.ResetTrafficByEmail(inbounds, email)
				return err
			}},
			{"bulk", func(_ *model.Inbound, email string) error {
				_, err := clients.BulkResetTraffic(inbounds, []string{email, email})
				return err
			}},
			{"periodic inbound", func(ib *model.Inbound, _ string) error { return clients.ResetAllClientTraffics(inbounds, ib.Id) }},
			{"all clients", func(_ *model.Inbound, _ string) error { return clients.ResetAllClientTraffics(inbounds, -1) }},
			{"all traffic", func(_ *model.Inbound, _ string) error { _, err := clients.ResetAllTraffics(inbounds); return err }},
			{"automatic renewal", func(_ *model.Inbound, _ string) error {
				return db.Transaction(func(tx *gorm.DB) error { _, _, _, err := inbounds.autoRenewClients(tx); return err })
			}},
			{"hand counter reset", func(_ *model.Inbound, email string) error { return inbounds.UpdateClientTrafficByEmail(email, 0, 0) }},
			{"hand counter replacement", func(_ *model.Inbound, email string) error { return inbounds.UpdateClientTrafficByEmail(email, 90, 80) }},
		}
		for i, reset := range resets {
			t.Run(reset.name, func(t *testing.T) {
				email := fmt.Sprintf("history-reset-%d", i)
				c := model.Client{Email: email, ID: "11111111-1111-1111-1111-111111111111", Enable: true}
				if reset.name == "automatic renewal" {
					c.Reset, c.ExpiryTime = 30, time.Now().Add(-time.Hour).UnixMilli()
				}
				ib := &model.Inbound{UserId: 1, Tag: email, Port: 48000 + i, Protocol: model.VLESS, Settings: clientsSettings(t, []model.Client{c})}
				if err := db.Create(ib).Error; err != nil {
					t.Fatal(err)
				}
				if err := clients.SyncInbound(db, ib.Id, []model.Client{c}); err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&core.ClientTraffic{Email: email, InboundId: ib.Id, Enable: true, Up: 110, Down: 220, Reset: c.Reset, ExpiryTime: c.ExpiryTime}).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&model.ClientHistoryCursor{Email: email, Up: 10, Down: 20}).Error; err != nil {
					t.Fatal(err)
				}
				if err := reset.run(ib, email); err != nil {
					t.Fatal(err)
				}
				if err := inbounds.addClientTraffic(db, []*core.ClientTraffic{{Email: email, Up: 150, Down: 250}}); err != nil {
					t.Fatal(err)
				}
				for range 2 {
					if err := RecordClientHistory(time.Now()); err != nil {
						t.Fatal(err)
					}
				}
				var total struct{ Up, Down int64 }
				if err := db.Model(&model.ClientTrafficSample{}).Select("COALESCE(SUM(up), 0) AS up, COALESCE(SUM(down), 0) AS down").Where("email = ?", email).Scan(&total).Error; err != nil {
					t.Fatal(err)
				}
				if total.Up != 250 || total.Down != 450 {
					t.Fatalf("history after reset/refill and two samples = %d/%d, want 250/450", total.Up, total.Down)
				}
			})
		}
	})

	t.Run("continuation exempts quota only", func(t *testing.T) {
		for i, tc := range []struct {
			name, action                      string
			expired, override, stale, enabled bool
		}{
			{"ordinary quota", "disable", false, false, false, false},
			{"notify quota", "notify", false, false, false, true},
			{"throttle quota", "throttle", false, false, false, true},
			{"expired notify", "notify", true, false, false, false},
			{"expired throttle", "throttle", true, false, false, false},
			{"overridden quota", "notify", false, true, false, false},
			{"pending template revision", "notify", false, false, true, true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				email := fmt.Sprintf("quota-branch-%d", i)
				p := model.Policy{Name: email, OnDepletion: tc.action, Rev: 1}
				if err := db.Create(&p).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&model.ClientRecord{Email: email, Enable: true}).Error; err != nil {
					t.Fatal(err)
				}
				a := model.ClientPolicy{Email: email, PolicyId: &p.Id, AppliedRev: 1, AppliedOnDepletion: tc.action, OverrideQuota: tc.override}
				if tc.stale {
					a.AppliedRev = 0
				}
				if err := db.Create(&a).Error; err != nil {
					t.Fatal(err)
				}
				ct := core.ClientTraffic{Email: email, Enable: true, Total: 100, Up: 100}
				if tc.expired {
					ct.ExpiryTime = time.Now().Add(-time.Hour).UnixMilli()
				}
				if err := db.Create(&ct).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Transaction(func(tx *gorm.DB) error { _, _, _, err := inbounds.disableInvalidClients(tx); return err }); err != nil {
					t.Fatal(err)
				}
				if err := db.Where("email = ?", email).First(&ct).Error; err != nil {
					t.Fatal(err)
				}
				if ct.Enable != tc.enabled {
					t.Fatalf("enable=%v, want %v", ct.Enable, tc.enabled)
				}
			})
		}
	})

	t.Run("owner budget prevents reset recovery", func(t *testing.T) {
		for i, tc := range []struct {
			name    string
			account model.ResellerAccount
		}{
			{"suspended", model.ResellerAccount{Suspended: true}},
			{"blocked", model.ResellerAccount{BudgetBlocked: true}},
			{"expired", model.ResellerAccount{ExpiresAt: time.Now().Add(-time.Hour).UnixMilli()}},
			{"depleted", model.ResellerAccount{TrafficLimit: 1, TrafficUsed: 1}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				email := fmt.Sprintf("owner-reset-%d", i)
				user := model.User{Username: email, Role: "reseller"}
				if err := db.Create(&user).Error; err != nil {
					t.Fatal(err)
				}
				tc.account.UserID = user.Id
				if err := db.Create(&tc.account).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&model.ClientRecord{Email: email, OwnerUserID: &user.Id, Enable: false}).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Model(&model.ClientRecord{}).Where("email = ?", email).Update("enable", false).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Create(&core.ClientTraffic{Email: email, Enable: false, Total: 100, Up: 100}).Error; err != nil {
					t.Fatal(err)
				}
				if err := inbounds.ResetClientTrafficByEmail(email); err != nil {
					t.Fatal(err)
				}
				if _, err := clients.ResetTrafficByEmail(inbounds, email); err != nil {
					t.Fatal(err)
				}
				if _, err := clients.BulkResetTraffic(inbounds, []string{email}); err != nil {
					t.Fatal(err)
				}
				var ct core.ClientTraffic
				if err := db.Where("email = ?", email).First(&ct).Error; err != nil {
					t.Fatal(err)
				}
				var c model.ClientRecord
				if err := db.Where("email = ?", email).First(&c).Error; err != nil {
					t.Fatal(err)
				}
				if ct.Enable || c.Enable || ct.Up != 0 {
					t.Fatalf("blocked owner recovered: traffic=%+v client=%+v", ct, c)
				}
			})
		}
	})
}
