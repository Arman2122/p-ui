package service

import (
	"errors"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestResellerTransferAndDeletePostgres(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()
	svc := &ResellerService{}
	admin := model.User{Username: "transfer-admin", Role: "admin"}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	day := int64(86400000)
	term := time.Now().UnixMilli() + 60*day
	granted := mkInbound(t, 43970, model.VLESS, `{"clients":[]}`)
	foreign := mkInbound(t, 43971, model.VMESS, `{"clients":[]}`)
	newReseller := func(name string, cap int) *model.User {
		t.Helper()
		u := &model.User{Username: name, Role: "reseller"}
		if err := db.Create(u).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.ResellerAccount{UserID: u.Id, Name: name, TrafficLimit: 10000, ClientCap: cap, ExpiresAt: term, AllowReset: true, AllowSuspend: true}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.ResellerInboundGrant{UserID: u.Id, InboundID: granted.Id}).Error; err != nil {
			t.Fatal(err)
		}
		return u
	}
	a, b := newReseller("owner-a", 0), newReseller("owner-b", 1)
	newClient := func(email string, inbound int, shape func(r *model.ClientRecord)) model.ClientRecord {
		t.Helper()
		rec := model.ClientRecord{Email: email, Enable: true, TotalGB: 100, ExpiryTime: term - day}
		shape(&rec)
		if err := db.Create(&rec).Error; err != nil {
			t.Fatal(err)
		}
		if inbound > 0 {
			if err := db.Create(&model.ClientInbound{ClientId: rec.Id, InboundId: inbound}).Error; err != nil {
				t.Fatal(err)
			}
		}
		if err := db.Create(&core.ClientTraffic{Email: email, Enable: true}).Error; err != nil {
			t.Fatal(err)
		}
		return rec
	}
	owner := func(id int) int {
		t.Helper()
		var r model.ClientRecord
		if err := db.First(&r, id).Error; err != nil {
			t.Fatal(err)
		}
		if r.OwnerUserID == nil {
			return 0
		}
		return *r.OwnerUserID
	}
	plans := func(email string) int64 {
		t.Helper()
		var n int64
		if err := db.Model(&model.ClientPolicy{}).Where("email = ?", email).Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n
	}
	noop := func(*model.ClientRecord) {}
	x := newClient("transfer-x", granted.Id, noop)

	t.Run("assignment is refused outside the grants, the term, the budget shape and the cap", func(t *testing.T) {
		if _, err := svc.AssignClients(a, a.Id, []int{x.Id}); !errors.Is(err, ErrResellerForbidden) {
			t.Fatalf("reseller assigned clients to itself: %v", err)
		}
		ungranted := newClient("transfer-ungranted", foreign.Id, noop)
		if _, err := svc.AssignClients(&admin, a.Id, []int{ungranted.Id}); !errors.Is(err, ErrResellerForbidden) {
			t.Fatalf("ungranted inbound accepted: %v", err)
		}
		endless := newClient("transfer-endless", granted.Id, func(r *model.ClientRecord) { r.ExpiryTime = 0 })
		if _, err := svc.AssignClients(&admin, a.Id, []int{endless.Id}); !errors.Is(err, ErrResellerBudget) {
			t.Fatalf("client outliving the term accepted: %v", err)
		}
		unlimited := newClient("transfer-unlimited", granted.Id, func(r *model.ClientRecord) { r.TotalGB = 0 })
		if _, err := svc.AssignClients(&admin, a.Id, []int{unlimited.Id}); !errors.Is(err, ErrResellerBudget) {
			t.Fatalf("unlimited quota under a finite budget accepted: %v", err)
		}
		grouped := newClient("transfer-grouped", granted.Id, func(r *model.ClientRecord) { r.Group = "pool" })
		if _, err := svc.AssignClients(&admin, a.Id, []int{grouped.Id}); !errors.Is(err, ErrResellerInvalid) {
			t.Fatalf("grouped client accepted: %v", err)
		}
		if _, err := svc.AssignClients(&admin, a.Id, []int{x.Id, 987654}); !errors.Is(err, ErrResellerInvalid) {
			t.Fatalf("unknown client accepted: %v", err)
		}
		for _, id := range []int{ungranted.Id, endless.Id, unlimited.Id, grouped.Id, x.Id} {
			if owner(id) != 0 {
				t.Fatalf("refused assignment moved client %d", id)
			}
		}
	})
	t.Run("assignment moves prospectively and clears plans the owner cannot see", func(t *testing.T) {
		plan := model.Policy{Name: "admin plan"}
		if err := db.Create(&plan).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.ClientPolicy{Email: x.Email, PolicyId: &plan.Id}).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&core.ClientTraffic{}).Where("email = ?", x.Email).Update("down", 500).Error; err != nil {
			t.Fatal(err)
		}
		res, err := svc.AssignClients(&admin, a.Id, []int{x.Id})
		if err != nil {
			t.Fatal(err)
		}
		if res.Moved != 1 || res.PlansCleared != 1 || owner(x.Id) != a.Id || plans(x.Email) != 0 {
			t.Fatalf("assignment result=%+v owner=%d plans=%d", res, owner(x.Id), plans(x.Email))
		}
		if err := db.Model(&core.ClientTraffic{}).Where("email = ?", x.Email).Update("down", 800).Error; err != nil {
			t.Fatal(err)
		}
		view, err := svc.Get(&admin, a.Id)
		if err != nil {
			t.Fatal(err)
		}
		if view.TrafficUsed != 300 || view.Account.TrafficAllocated != x.TotalGB || view.Clients != 1 {
			t.Fatalf("prospective accounting: used=%d allocated=%d clients=%d", view.TrafficUsed, view.Account.TrafficAllocated, view.Clients)
		}
	})
	t.Run("a move between resellers respects the target cap", func(t *testing.T) {
		newClient("transfer-b-existing", granted.Id, func(r *model.ClientRecord) { r.OwnerUserID = &b.Id })
		if _, err := svc.AssignClients(&admin, b.Id, []int{x.Id}); !errors.Is(err, ErrResellerBudget) {
			t.Fatalf("cap ignored: %v", err)
		}
		if err := db.Model(&model.ResellerAccount{}).Where("user_id = ?", b.Id).Update("client_cap", 5).Error; err != nil {
			t.Fatal(err)
		}
		res, err := svc.AssignClients(&admin, b.Id, []int{x.Id})
		if err != nil {
			t.Fatal(err)
		}
		if res.Moved != 1 || owner(x.Id) != b.Id {
			t.Fatalf("move between resellers: %+v owner=%d", res, owner(x.Id))
		}
		if res, err := svc.AssignClients(&admin, b.Id, []int{x.Id}); err != nil || res.Moved != 0 {
			t.Fatalf("re-assigning an owned client: %+v %v", res, err)
		}
	})
	t.Run("release hands clients back and clears private plans", func(t *testing.T) {
		private := model.Policy{Name: "b private", OwnerUserID: &b.Id}
		if err := db.Create(&private).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.ClientPolicy{Email: x.Email, PolicyId: &private.Id}).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := svc.ReleaseClients(&admin, a.Id, []int{x.Id}); !errors.Is(err, ErrResellerForbidden) {
			t.Fatalf("released a client another reseller owns: %v", err)
		}
		res, err := svc.ReleaseClients(&admin, b.Id, []int{x.Id})
		if err != nil {
			t.Fatal(err)
		}
		if res.Moved != 1 || res.PlansCleared != 1 || owner(x.Id) != 0 || plans(x.Email) != 0 {
			t.Fatalf("release result=%+v owner=%d plans=%d", res, owner(x.Id), plans(x.Email))
		}
	})
	t.Run("deleting with a transfer keeps the clients and removes the rest", func(t *testing.T) {
		kept := newClient("transfer-kept", granted.Id, func(r *model.ClientRecord) { r.OwnerUserID = &a.Id })
		if _, err := svc.CreateToken(a, "automation", time.Now().Add(time.Hour).UnixMilli()); err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.Policy{Name: "a private", OwnerUserID: &a.Id}).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := (ActivityService{}).Record(t.Context(), model.ActivityEvent{AudienceUserID: a.Id, Kind: "audit.test", Level: "info"}, nil); err != nil {
			t.Fatal(err)
		}
		if err := svc.Delete(&admin, a.Id, ResellerDeleteRequest{Clients: "archive"}); !errors.Is(err, ErrResellerInvalid) {
			t.Fatalf("bad disposition accepted: %v", err)
		}
		if err := svc.Delete(&admin, a.Id, ResellerDeleteRequest{Clients: "transfer", OwnerID: a.Id}); !errors.Is(err, ErrResellerInvalid) {
			t.Fatalf("transfer to self accepted: %v", err)
		}
		if err := svc.Delete(&admin, a.Id, ResellerDeleteRequest{Clients: "transfer"}); err != nil {
			t.Fatal(err)
		}
		if owner(kept.Id) != 0 {
			t.Fatalf("transferred client owner=%d", owner(kept.Id))
		}
		for table, where := range map[string]any{
			"reseller_accounts": "user_id", "users": "id", "reseller_tokens": "user_id", "reseller_inbound_grants": "user_id",
			"reseller_allocations": "user_id", "policies": "owner_user_id", "activity_events": "audience_user_id",
		} {
			var n int64
			if err := db.Table(table).Where(where.(string)+" = ?", a.Id).Count(&n).Error; err != nil {
				t.Fatal(err)
			}
			if n != 0 {
				t.Fatalf("%s still holds %d rows for the deleted reseller", table, n)
			}
		}
	})
	t.Run("deleting with client deletion removes the owned clients through the runtime", func(t *testing.T) {
		created, err := svc.CreateClient(b, ResellerClientInput{Email: "transfer-doomed", TotalGB: 10, ExpiryTime: term - day, Enable: true, InboundIDs: []int{granted.Id}})
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.Delete(&admin, b.Id, ResellerDeleteRequest{Clients: "delete"}); err != nil {
			t.Fatal(err)
		}
		var n int64
		if err := db.Model(&model.ClientRecord{}).Where("id IN ?", []int{created.ClientID, x.Id}).Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&model.ClientRecord{}).Where("id = ?", created.ClientID).Count(&n).Error; err != nil || n != 0 {
			t.Fatalf("owned client survived the deletion: n=%d err=%v", n, err)
		}
		if err := db.Model(&model.User{}).Where("id = ?", b.Id).Count(&n).Error; err != nil || n != 0 {
			t.Fatalf("reseller login survived: n=%d err=%v", n, err)
		}
		if _, err := svc.Get(&admin, b.Id); err == nil {
			t.Fatal("deleted reseller still readable")
		}
	})
}
