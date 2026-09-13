package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/util/crypto"
	"github.com/Arman2122/p-ui/internal/web/runtime"
)

type resellerAccountingRuntime struct {
	runtime.Runtime
	check func() error
}

func (r resellerAccountingRuntime) UpdateUser(context.Context, *model.Inbound, string, model.Client) error {
	return r.check()
}

func TestResellerServicesPostgres(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()
	svc := &ResellerService{}
	admin := model.User{Username: "reseller-test-admin", Role: "admin"}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	newUser := func(name string) *model.User {
		t.Helper()
		u := &model.User{Username: name, Role: "reseller"}
		if err := db.Create(u).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.ResellerAccount{UserID: u.Id, Name: name, TrafficLimit: 1000, ClientCap: 20, ExpiresAt: time.Now().Add(48 * time.Hour).UnixMilli(), AllowReset: true, AllowExtend: true, AllowSuspend: true, AllowTopUp: true}).Error; err != nil {
			t.Fatal(err)
		}
		return u
	}
	user := newUser("reseller-test-one")
	other := newUser("reseller-test-two")
	ib := mkInbound(t, 43991, model.VLESS, `{"clients":[]}`)
	if err := db.Create(&model.ResellerInboundGrant{UserID: user.Id, InboundID: ib.Id}).Error; err != nil {
		t.Fatal(err)
	}
	input := ResellerClientInput{Email: "reseller-owned", TotalGB: 2000, ExpiryTime: time.Now().Add(time.Hour).UnixMilli(), Enable: false, InboundIDs: []int{ib.Id}}
	created, err := svc.CreateClient(user, input)
	if err != nil {
		t.Fatalf("create finite quota larger than aggregate account cap: %v", err)
	}
	if created.ClientID <= 0 {
		t.Fatalf("client ID=%d", created.ClientID)
	}
	readUsage := func(want int64) {
		t.Helper()
		var a model.ResellerAccount
		if err := db.First(&a, "user_id = ?", user.Id).Error; err != nil {
			t.Fatal(err)
		}
		if a.TrafficUsed != want {
			t.Fatalf("actual usage=%d want%d", a.TrafficUsed, want)
		}
	}
	t.Run("ownership and grants", func(t *testing.T) {
		if _, err := svc.OwnedClient(other, created.ClientID); !errors.Is(err, ErrResellerForbidden) {
			t.Fatalf("cross owner=%v", err)
		}
		if err := svc.DeleteClient(other, created.ClientID); !errors.Is(err, ErrResellerForbidden) {
			t.Fatalf("cross delete=%v", err)
		}
		if _, err := svc.ClientLinks(other, created.ClientID, "localhost"); !errors.Is(err, ErrResellerForbidden) {
			t.Fatalf("cross links=%v", err)
		}
		if _, err := svc.Clients(other, user.Id, 1, 50, "", 0); !errors.Is(err, ErrResellerForbidden) {
			t.Fatalf("cross listing=%v", err)
		}
		page, err := svc.Clients(user, user.Id, 1, 50, "", 0)
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 1 || page.Items[0].ID != created.ClientID {
			t.Fatalf("owned page=%+v", page)
		}
		private := model.Policy{Name: "Reseller filter", OwnerUserID: &user.Id, Tiers: "[]", Rev: 1}
		if err := database.GetDB().Create(&private).Error; err != nil {
			t.Fatal(err)
		}
		if err := database.GetDB().Create(&model.ClientPolicy{Email: input.Email, PolicyId: &private.Id}).Error; err != nil {
			t.Fatal(err)
		}
		if onPlan, err := svc.Clients(user, user.Id, 1, 50, "", private.Id); err != nil || onPlan.Total != 1 {
			t.Fatalf("plan filter page=%+v err=%v", onPlan, err)
		}
		if offPlan, err := svc.Clients(user, user.Id, 1, 50, "", private.Id+1000); err != nil || offPlan.Total != 0 {
			t.Fatalf("unknown plan filter page=%+v err=%v", offPlan, err)
		}
		bad := input
		bad.Email = "ungranted"
		if _, err := svc.CreateClient(other, bad); !errors.Is(err, ErrResellerForbidden) {
			t.Fatalf("ungranted create=%v", err)
		}
		bad.Email = "past-account-term"
		bad.ExpiryTime = time.Now().Add(72 * time.Hour).UnixMilli()
		if _, err := svc.CreateClient(user, bad); !errors.Is(err, ErrResellerBudget) {
			t.Fatalf("expiry cap=%v", err)
		}
		bad = input
		bad.Email = "unlimited-budget-bypass"
		bad.TotalGB = 0
		if _, err := svc.CreateClient(user, bad); !errors.Is(err, ErrResellerBudget) {
			t.Fatalf("unlimited cap=%v", err)
		}
	})
	t.Run("lifetime usage reset replay and mirrored snapshots", func(t *testing.T) {
		if err := db.Model(&core.ClientTraffic{}).Where("email = ?", input.Email).Updates(map[string]any{"up": 100, "down": 20}).Error; err != nil {
			t.Fatal(err)
		}
		readUsage(120)
		global := model.ClientGlobalTraffic{MasterGuid: "master-a", Email: input.Email, Up: 100, Down: 20, UpdatedAt: time.Now().UnixMilli()}
		if err := db.Create(&global).Error; err != nil {
			t.Fatal(err)
		}
		readUsage(120)
		if err := db.Model(&global).Updates(map[string]any{"up": 130, "down": 20}).Error; err != nil {
			t.Fatal(err)
		}
		readUsage(150)
		if err := db.Model(&core.ClientTraffic{}).Where("email = ?", input.Email).Updates(map[string]any{"up": 130, "down": 20}).Error; err != nil {
			t.Fatal(err)
		}
		readUsage(150)
		if err := db.Delete(&global).Error; err != nil {
			t.Fatal(err)
		}
		global.Id = 0
		if err := db.Create(&global).Error; err != nil {
			t.Fatal(err)
		}
		readUsage(150)
		if err := db.Model(&core.ClientTraffic{}).Where("email = ?", input.Email).Updates(map[string]any{"up": 0, "down": 0}).Error; err != nil {
			t.Fatal(err)
		}
		readUsage(150)
		if err := db.Model(&core.ClientTraffic{}).Where("email = ?", input.Email).Update("up", 10).Error; err != nil {
			t.Fatal(err)
		}
		readUsage(160)
		migration, err := os.ReadFile("../../database/reseller_usage.sql")
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Transaction(func(tx *gorm.DB) error { return tx.Exec(string(migration)).Error }); err != nil {
			t.Fatal(err)
		}
		readUsage(160)
	})
	t.Run("database grants reject indirect group and attachment bypass", func(t *testing.T) {
		forbidden := &model.Inbound{Tag: "reseller-forbidden-attachment", Enable: true, Protocol: model.VLESS, Port: 43996, Settings: `{"clients":[]}`}
		if err := db.Create(forbidden).Error; err != nil {
			t.Fatal(err)
		}
		assertState := func(err error, want string) {
			t.Helper()
			var state interface{ SQLState() string }
			if !errors.As(err, &state) || state.SQLState() != want {
				t.Fatalf("SQL error=%v want%s", err, want)
			}
		}
		assertState(db.Create(&model.ClientInbound{ClientId: created.ClientID, InboundId: forbidden.Id}).Error, "23503")
		assertState(db.Model(&model.ClientRecord{}).Where("id = ?", created.ClientID).Update("group_name", "administrator-group").Error, "23514")
		assertState(db.Where("user_id = ? AND inbound_id = ?", user.Id, ib.Id).Delete(&model.ResellerInboundGrant{}).Error, "23503")
		assertState(db.Model(&model.ClientRecord{}).Where("id = ?", created.ClientID).Update("owner_user_id", other.Id).Error, "23503")
		if err := ValidateClientOwnerInbounds(input.Email, []int{ib.Id, forbidden.Id}); !errors.Is(err, ErrResellerForbidden) {
			t.Fatalf("full grant set=%v", err)
		}
		if err := ValidateClientOwnerGroup(input.Email, "administrator-group"); !errors.Is(err, ErrResellerForbidden) {
			t.Fatalf("global group=%v", err)
		}
	})
	t.Run("owner transfer is prospective", func(t *testing.T) {
		if err := db.Create(&model.ResellerInboundGrant{UserID: other.Id, InboundID: ib.Id}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&model.ClientRecord{}).Where("id = ?", created.ClientID).Update("owner_user_id", other.Id).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&core.ClientTraffic{}).Where("email = ?", input.Email).Update("up", 20).Error; err != nil {
			t.Fatal(err)
		}
		readUsage(160)
		var a model.ResellerAccount
		if err := db.First(&a, "user_id = ?", other.Id).Error; err != nil {
			t.Fatal(err)
		}
		if a.TrafficUsed != 10 {
			t.Fatalf("transferred historical usage=%d want10", a.TrafficUsed)
		}
		if err := svc.DeleteClient(other, created.ClientID); err != nil {
			t.Fatal(err)
		}
		if err := db.First(&a, "user_id = ?", other.Id).Error; err != nil {
			t.Fatal(err)
		}
		if a.TrafficUsed != 10 {
			t.Fatalf("delete refunded usage=%d", a.TrafficUsed)
		}
	})
	t.Run("parallel provisioning respects client cap", func(t *testing.T) {
		if err := db.Model(&model.ResellerAccount{}).Where("user_id = ?", user.Id).Update("client_cap", 1).Error; err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		results := make(chan error, 2)
		for i := range 2 {
			wg.Go(func() {
				v := input
				v.Email = fmt.Sprintf("parallel-%d", i)
				_, err := svc.CreateClient(user, v)
				results <- err
			})
		}
		wg.Wait()
		close(results)
		ok, denied := 0, 0
		for err := range results {
			if err == nil {
				ok++
			} else if errors.Is(err, ErrResellerBudget) {
				denied++
			} else {
				t.Fatal(err)
			}
		}
		if ok != 1 || denied != 1 {
			t.Fatalf("success=%d budget refusals=%d", ok, denied)
		}
	})
	t.Run("owned tokens and password invalidate inherited access", func(t *testing.T) {
		token, err := svc.CreateToken(user, "test", time.Now().Add(time.Hour).UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.DeleteToken(other, token.ID); !errors.Is(err, ErrResellerForbidden) {
			t.Fatalf("token cross-owner=%v", err)
		}
		if _, err := svc.MatchToken(token.Token); err != nil {
			t.Fatal(err)
		}
		hash, err := crypto.HashPasswordAsBcrypt("old-password-for-test")
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Model(user).Update("password", hash).Error; err != nil {
			t.Fatal(err)
		}
		if err := svc.ChangePassword(user, "old-password-for-test", "new-password-for-test"); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.MatchToken(token.Token); !errors.Is(err, ErrResellerForbidden) {
			t.Fatalf("stale token=%v", err)
		}
	})
	t.Run("remote only usage is merged once", func(t *testing.T) {
		u := newUser("remote-reseller")
		nodeID, _ := setupNodeRuntime(t)
		client := model.Client{Email: "reseller-remote", ID: "c6c6d6ae-fc1a-4b3b-883b-f21ad7fef003", Enable: true, TotalGB: 2000}
		remote := nodeInbound(t, nodeID, 43993, []model.Client{client})
		if err := db.Create(&model.ResellerInboundGrant{UserID: u.Id, InboundID: remote.Id}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&model.ClientRecord{}).Where("email = ?", client.Email).Update("owner_user_id", u.Id).Error; err != nil {
			t.Fatal(err)
		}
		for _, bytes := range []int64{0, 100, 100, 150} {
			snap := &runtime.TrafficSnapshot{Inbounds: []*model.Inbound{{Tag: remote.Tag, Settings: remote.Settings, ClientStats: []core.ClientTraffic{{Email: client.Email, Up: bytes, Enable: true, Total: 2000}}}}}
			if _, err := (&InboundService{}).SetRemoteTraffic(nodeID, snap, false); err != nil {
				t.Fatal(err)
			}
		}
		var a model.ResellerAccount
		if err := db.First(&a, "user_id = ?", u.Id).Error; err != nil {
			t.Fatal(err)
		}
		if a.TrafficUsed != 150 {
			t.Fatalf("remote lifetime=%d want150", a.TrafficUsed)
		}
		var rec model.ClientRecord
		if err := db.First(&rec, "email = ?", client.Email).Error; err != nil {
			t.Fatal(err)
		}
		if rec.OwnerUserID == nil || *rec.OwnerUserID != u.Id {
			t.Fatalf("node sync lost owner: %v", rec.OwnerUserID)
		}
	})
	t.Run("legacy edit rename import reset and sync preserve owner", func(t *testing.T) {
		u := newUser("preserved-reseller")
		local := &model.Inbound{Tag: "reseller-owner-preservation", Enable: true, Port: 43995, Protocol: model.VLESS, Settings: `{"clients":[]}`}
		if err := db.Create(local).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.ResellerInboundGrant{UserID: u.Id, InboundID: local.Id}).Error; err != nil {
			t.Fatal(err)
		}
		created, err := svc.CreateClient(u, ResellerClientInput{Email: "preserved-old", TotalGB: 2000, ExpiryTime: time.Now().Add(time.Hour).UnixMilli(), Enable: true, InboundIDs: []int{local.Id}})
		if err != nil {
			t.Fatal(err)
		}
		cs, ibs := &ClientService{}, &InboundService{}
		assertOwner := func(email string) *model.ClientRecord {
			t.Helper()
			var rec model.ClientRecord
			if err := db.First(&rec, created.ClientID).Error; err != nil {
				t.Fatal(err)
			}
			if rec.Email != email || rec.OwnerUserID == nil || *rec.OwnerUserID != u.Id {
				t.Fatalf("owner lost: email=%s owner=%v", rec.Email, rec.OwnerUserID)
			}
			return &rec
		}
		rec := assertOwner("preserved-old")
		edit := rec.ToClient()
		edit.Comment = "edited through legacy seam"
		if _, err := cs.UpdateByEmail(ibs, rec.Email, *edit); err != nil {
			t.Fatal(err)
		}
		assertOwner(rec.Email)
		edit.Email = "preserved-new"
		if _, err := cs.UpdateByEmail(ibs, rec.Email, *edit); err != nil {
			t.Fatal(err)
		}
		rec = assertOwner(edit.Email)
		if _, _, err := cs.ImportClients(ibs, []ClientCreatePayload{{Client: *rec.ToClient(), InboundIds: []int{local.Id}}}); err != nil {
			t.Fatal(err)
		}
		rec = assertOwner(edit.Email)
		if err := db.Model(&core.ClientTraffic{}).Where("email = ?", rec.Email).Update("up", 100).Error; err != nil {
			t.Fatal(err)
		}
		if err := svc.ResetClient(u, rec.Id); err != nil {
			t.Fatal(err)
		}
		rec = assertOwner(edit.Email)
		if err := cs.SyncInbound(nil, local.Id, []model.Client{*rec.ToClient()}); err != nil {
			t.Fatal(err)
		}
		assertOwner(edit.Email)
		if err := db.Model(&core.ClientTraffic{}).Where("email = ?", rec.Email).Update("up", 10).Error; err != nil {
			t.Fatal(err)
		}
		var a model.ResellerAccount
		if err := db.First(&a, "user_id = ?", u.Id).Error; err != nil {
			t.Fatal(err)
		}
		if a.TrafficUsed != 110 {
			t.Fatalf("usage after legacy lifecycle=%d want110", a.TrafficUsed)
		}
	})
	t.Run("owner assignment excludes old counters from a late master snapshot", func(t *testing.T) {
		u := newUser("late-master-reseller")
		rec := model.ClientRecord{Email: "late-master-client", SubID: "late-master-sub", Enable: true, TotalGB: 2000}
		if err := db.Create(&rec).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&core.ClientTraffic{Email: rec.Email, Up: 100, Enable: true}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&rec).Update("owner_user_id", u.Id).Error; err != nil {
			t.Fatal(err)
		}
		global := model.ClientGlobalTraffic{Email: rec.Email, MasterGuid: "late-master", Up: 100, UpdatedAt: time.Now().UnixMilli()}
		if err := db.Create(&global).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&global).Update("up", 110).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&core.ClientTraffic{}).Where("email = ?", rec.Email).Update("up", 110).Error; err != nil {
			t.Fatal(err)
		}
		var a model.ResellerAccount
		if err := db.First(&a, "user_id = ?", u.Id).Error; err != nil {
			t.Fatal(err)
		}
		if a.TrafficUsed != 10 {
			t.Fatalf("late mirror charged historical usage: %d want10", a.TrafficUsed)
		}
	})
	t.Run("administrator inbound deletion cascades grants and preserves spent usage", func(t *testing.T) {
		u := newUser("delete-inbound-reseller")
		ib := &model.Inbound{Tag: "owned-inbound-delete", Enable: true, Port: 43997, Protocol: model.VLESS, Settings: `{"clients":[]}`}
		if err := db.Create(ib).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.ResellerInboundGrant{UserID: u.Id, InboundID: ib.Id}).Error; err != nil {
			t.Fatal(err)
		}
		created, err := svc.CreateClient(u, ResellerClientInput{Email: "inbound-delete-client", TotalGB: 2000, ExpiryTime: time.Now().Add(time.Hour).UnixMilli(), Enable: true, InboundIDs: []int{ib.Id}})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&core.ClientTraffic{}).Where("email = ?", "inbound-delete-client").Update("up", 100).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := (&InboundService{}).DelInbound(ib.Id); err != nil {
			t.Fatalf("owned inbound deletion: %v", err)
		}
		var n int64
		if err := db.Model(&model.ResellerInboundGrant{}).Where("user_id = ? AND inbound_id = ?", u.Id, ib.Id).Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("stale grant count=%d", n)
		}
		if err := db.Model(&model.ClientInbound{}).Where("client_id = ? AND inbound_id = ?", created.ClientID, ib.Id).Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("stale attachment count=%d", n)
		}
		var a model.ResellerAccount
		if err := db.First(&a, "user_id = ?", u.Id).Error; err != nil {
			t.Fatal(err)
		}
		if a.TrafficUsed != 100 {
			t.Fatalf("inbound deletion refunded usage: %d", a.TrafficUsed)
		}
	})
	t.Run("runtime dispatch releases budget rows and budget enforcement restores selectively", func(t *testing.T) {
		u := newUser("concurrent-reseller")
		nodeID, fake := setupNodeRuntime(t)
		client := model.Client{Email: "concurrent-usage", ID: "e9852460-7961-49b0-8677-72a12a935d55", SubID: "concurrent-sub", Enable: true, TotalGB: 3000, ExpiryTime: time.Now().Add(time.Hour).UnixMilli()}
		ib := nodeInbound(t, nodeID, 43994, []model.Client{client})
		if err := db.Create(&model.ResellerInboundGrant{UserID: u.Id, InboundID: ib.Id}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&model.ClientRecord{}).Where("email = ?", client.Email).Update("owner_user_id", u.Id).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&core.ClientTraffic{Email: client.Email, Enable: true, Total: 3000}).Error; err != nil {
			t.Fatal(err)
		}
		StartTrafficWriter()
		defer StopTrafficWriter()
		observed := make(chan error, 1)
		runtime.GetManager().SetRuntimeOverride(nodeID, resellerAccountingRuntime{Runtime: fake, check: func() error {
			err := submitTrafficWrite(func() error {
				ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
				defer cancel()
				return db.WithContext(ctx).Model(&core.ClientTraffic{}).Where("email = ?", client.Email).Update("up", 100).Error
			})
			observed <- err
			return err
		}})
		var rec model.ClientRecord
		if err := db.First(&rec, "email = ?", client.Email).Error; err != nil {
			t.Fatal(err)
		}
		_, err := svc.UpdateClient(u, rec.Id, ResellerClientInput{Email: client.Email, TotalGB: 4000, ExpiryTime: client.ExpiryTime, Enable: true, InboundIDs: []int{ib.Id}})
		if err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-observed:
			if err != nil {
				t.Fatalf("writer waited on account row across runtime: %v", err)
			}
		default:
			t.Fatal("runtime path was not reached")
		}
		runtime.GetManager().SetRuntimeOverride(nodeID, fake)
		if err := db.Model(&core.ClientTraffic{}).Where("email = ?", client.Email).Update("up", 1100).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := svc.EnforceBudgets(t.Context()); err != nil {
			t.Fatal(err)
		}
		if err := db.First(&rec, rec.Id).Error; err != nil {
			t.Fatal(err)
		}
		if rec.Enable {
			t.Fatal("depleted account client remained enabled")
		}
		if err := svc.TopUp(&admin, u.Id, 1000, 0, 0); err != nil {
			t.Fatal(err)
		}
		if err := db.First(&rec, rec.Id).Error; err != nil {
			t.Fatal(err)
		}
		if !rec.Enable {
			t.Fatal("topup failed to restore budget-paused client")
		}
		if rec.OwnerUserID == nil || *rec.OwnerUserID != u.Id {
			t.Fatalf("budget lifecycle lost owner: %v", rec.OwnerUserID)
		}
		var a model.ResellerAccount
		if err := db.First(&a, "user_id = ?", u.Id).Error; err != nil {
			t.Fatal(err)
		}
		if a.TrafficUsed != 1100 {
			t.Fatalf("topup changed actual spent usage: %d", a.TrafficUsed)
		}
		if err := db.Model(&model.Node{}).Where("id = ?", nodeID).Updates(map[string]any{"status": "offline", "config_dirty": false}).Error; err != nil {
			t.Fatal(err)
		}
		if err := svc.Suspend(&admin, u.Id, true); err != nil {
			t.Fatal(err)
		}
		_, _, dirty, _, err := (&NodeService{}).NodeSyncState(nodeID)
		if err != nil {
			t.Fatal(err)
		}
		if !dirty {
			t.Fatal("offline suspension lost durable reconciliation marker")
		}
		if _, err := svc.EnforceBudgets(t.Context()); err != nil {
			t.Fatal(err)
		}
		_, _, dirty, _, err = (&NodeService{}).NodeSyncState(nodeID)
		if err != nil {
			t.Fatal(err)
		}
		if !dirty {
			t.Fatal("idempotent pass cleared pending node changes")
		}
	})
}
