package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func resellerReservationFixture(t *testing.T, name string) (*model.User, *model.Inbound) {
	t.Helper()
	db := database.GetDB()
	u := &model.User{Username: name, Role: "reseller"}
	if err := db.Create(u).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ResellerAccount{UserID: u.Id, Name: name, TrafficLimit: 10000, ClientCap: 20, ExpiresAt: time.Now().Add(72 * time.Hour).UnixMilli(), AllowReset: true, AllowExtend: true, AllowSuspend: true, AllowTopUp: true}).Error; err != nil {
		t.Fatal(err)
	}
	ib := &model.Inbound{Tag: name, Enable: true, Port: 45000 + u.Id, Protocol: model.VLESS, Settings: `{"clients":[]}`}
	if err := db.Create(ib).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ResellerInboundGrant{UserID: u.Id, InboundID: ib.Id}).Error; err != nil {
		t.Fatal(err)
	}
	return u, ib
}

func assertResellerAllocations(t *testing.T, userID int, bytes int64, entries int64) {
	t.Helper()
	var user model.User
	db := database.GetDB()
	if err := db.First(&user, userID).Error; err != nil {
		t.Fatal(err)
	}
	view, err := (&ResellerService{}).Get(&user, userID)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Bytes   int64
		Entries int64
	}
	if err := db.Model(&model.ResellerAllocation{}).Where("user_id = ?", userID).Select("COALESCE(SUM(bytes),0) AS bytes, COUNT(*) AS entries").Scan(&got).Error; err != nil {
		t.Fatal(err)
	}
	if view.Account.TrafficAllocated != bytes || got.Bytes != bytes || got.Entries != entries {
		t.Fatalf("allocation account=%d ledger=%d entries=%d want%d/%d", view.Account.TrafficAllocated, got.Bytes, got.Entries, bytes, entries)
	}
}

func TestResellerPageAndReservationsPostgres(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()
	svc := &ResellerService{}
	t.Run("quota allocation never blocks the traffic writer on account counters", func(t *testing.T) {
		u, ib := resellerReservationFixture(t, "reseller-lock-order")
		input := ResellerClientInput{Email: "reseller-lock-order-client", TotalGB: 100, ExpiryTime: time.Now().Add(time.Hour).UnixMilli(), Enable: true, InboundIDs: []int{ib.Id}}
		created, err := svc.CreateClient(u, input)
		if err != nil {
			t.Fatal(err)
		}
		StartTrafficWriter()
		defer StopTrafficWriter()
		ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
		defer cancel()
		trafficLocked, quotaChanged := make(chan struct{}), make(chan struct{})
		writerDone := make(chan error, 1)
		go func() {
			writerDone <- submitTrafficWrite(func() error {
				return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
					var id int
					if err := tx.Raw("SELECT id FROM client_traffics WHERE email = ? FOR UPDATE", input.Email).Scan(&id).Error; err != nil {
						return err
					}
					close(trafficLocked)
					select {
					case <-quotaChanged:
					case <-ctx.Done():
						return ctx.Err()
					}
					return tx.Model(&core.ClientTraffic{}).Where("id = ?", id).Update("up", 10).Error
				})
			})
		}()
		select {
		case <-trafficLocked:
		case err := <-writerDone:
			t.Fatalf("writer failed to lock traffic: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		quotaDone := make(chan error, 1)
		go func() {
			quotaDone <- db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
				if err := tx.Model(&model.ClientRecord{}).Where("id = ?", created.ClientID).Update("total_gb", 200).Error; err != nil {
					return err
				}
				close(quotaChanged)
				return tx.Model(&core.ClientTraffic{}).Where("email = ?", input.Email).Update("total", 200).Error
			})
		}()
		for name, done := range map[string]<-chan error{"writer": writerDone, "quota": quotaDone} {
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("%s lock inversion: %v", name, err)
				}
			case <-ctx.Done():
				t.Fatalf("%s blocked: %v", name, ctx.Err())
			}
		}
		assertResellerAllocations(t, u.Id, 200, 2)
		var account model.ResellerAccount
		if err := db.First(&account, "user_id = ?", u.Id).Error; err != nil {
			t.Fatal(err)
		}
		if account.TrafficUsed != 10 {
			t.Fatalf("concurrent usage=%d want10", account.TrafficUsed)
		}
		if err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := checkResellerBudget(tx, &account); err != nil {
				return err
			}
			allocated := make(chan error, 1)
			go func() {
				allocated <- db.WithContext(ctx).Model(&model.ClientRecord{}).Where("id = ?", created.ClientID).Update("total_gb", 300).Error
			}()
			select {
			case err := <-allocated:
				return err
			case <-ctx.Done():
				return ctx.Err()
			}
		}); err != nil {
			t.Fatalf("budget row lock blocked allocation FK: %v", err)
		}
		assertResellerAllocations(t, u.Id, 300, 3)
	})
	t.Run("canonical usage and constant membership query count", func(t *testing.T) {
		u, ib := resellerReservationFixture(t, "reseller-page")
		other, otherIB := resellerReservationFixture(t, "reseller-page-other")
		owned := []model.ClientRecord{}
		for i := range 3 {
			c := model.ClientRecord{Email: fmt.Sprintf("reseller-page-%d", i), OwnerUserID: &u.Id, Enable: true, TotalGB: 100, ExpiryTime: time.Now().Add(48 * time.Hour).UnixMilli()}
			if err := db.Create(&c).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&model.ClientInbound{ClientId: c.Id, InboundId: ib.Id}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&core.ClientTraffic{Email: c.Email, Up: 40, Down: 10, Enable: true, LastOnline: time.Now().UnixMilli()}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&model.ClientGlobalTraffic{Email: c.Email, MasterGuid: "fresh-master", Up: 100, Down: 20, UpdatedAt: time.Now().UnixMilli()}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&model.ClientGlobalTraffic{Email: c.Email, MasterGuid: "stale-master", Up: 900, UpdatedAt: time.Now().Add(-48 * time.Hour).UnixMilli()}).Error; err != nil {
				t.Fatal(err)
			}
			owned = append(owned, c)
		}
		outsider := model.ClientRecord{Email: "page-outsider", OwnerUserID: &other.Id, Enable: true, TotalGB: 100}
		if err := db.Create(&outsider).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.ClientInbound{ClientId: outsider.Id, InboundId: otherIB.Id}).Error; err != nil {
			t.Fatal(err)
		}
		ownPlan := model.Policy{Name: "Private owned preset", OwnerUserID: &u.Id, Tiers: "[]", Rev: 2}
		foreignPlan := model.Policy{Name: "Other reseller private preset", OwnerUserID: &other.Id, Tiers: "[]", Rev: 1}
		for _, plan := range []*model.Policy{&ownPlan, &foreignPlan} {
			if err := db.Create(plan).Error; err != nil {
				t.Fatal(err)
			}
		}
		for _, assignment := range []model.ClientPolicy{{Email: owned[0].Email, PolicyId: &ownPlan.Id, AppliedRev: 1, OverrideQuota: true}, {Email: owned[1].Email, PolicyId: &foreignPlan.Id, AppliedRev: 0}} {
			if err := db.Create(&assignment).Error; err != nil {
				t.Fatal(err)
			}
		}
		now := time.Now().Unix() / 3600 * 3600
		for _, sample := range []model.ClientTrafficSample{{Email: owned[0].Email, T: now, Up: 7, Down: 11}, {Email: outsider.Email, T: now, Up: 99999}} {
			if err := db.Create(&sample).Error; err != nil {
				t.Fatal(err)
			}
		}
		var queries atomic.Int64
		count := func(*gorm.DB) { queries.Add(1) }
		if err := db.Callback().Query().After("gorm:query").Register("reseller-page-count", count); err != nil {
			t.Fatal(err)
		}
		if err := db.Callback().Row().After("gorm:row").Register("reseller-page-count", count); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			_ = db.Callback().Query().Remove("reseller-page-count")
			_ = db.Callback().Row().Remove("reseller-page-count")
		})
		small, err := svc.Clients(u, u.Id, 1, 1, "")
		if err != nil {
			t.Fatal(err)
		}
		smallQueries := queries.Swap(0)
		large, err := svc.Clients(u, u.Id, 1, 200, "")
		if err != nil {
			t.Fatal(err)
		}
		largeQueries := queries.Load()
		if smallQueries != largeQueries {
			t.Fatalf("queries grew with page rows: one=%d three=%d", smallQueries, largeQueries)
		}
		if small.Total != 3 || len(small.Items) != 1 || small.Items[0].ID != owned[2].Id || large.Total != 3 || len(large.Items) != 3 {
			t.Fatalf("page isolation small=%+v large=%+v", small, large)
		}
		for _, row := range large.Items {
			if row.TrafficUsed != 120 || !slices.Equal(row.InboundIDs, []int{ib.Id}) {
				t.Fatalf("canonical owned row=%+v", row)
			}
			switch row.Email {
			case owned[0].Email:
				if row.Plan == nil || row.Plan.Name != ownPlan.Name || !row.Plan.Pending || !row.Plan.Overridden {
					t.Fatalf("owned plan metadata=%+v", row.Plan)
				}
				if len(row.History) != 1 || row.History[0].Up != 7 || row.History[0].Down != 11 {
					t.Fatalf("owned history=%+v", row.History)
				}
			case owned[1].Email:
				if row.Plan != nil || len(row.History) != 0 {
					t.Fatalf("cross-owner metadata escaped: %+v history=%+v", row.Plan, row.History)
				}
			default:
				if row.Plan != nil || len(row.History) != 0 {
					t.Fatalf("off-page enrichment escaped: %+v", row)
				}
			}
		}
		inbounds, err := svc.Inbounds(u)
		if err != nil {
			t.Fatal(err)
		}
		if len(inbounds) != 1 || inbounds[0].TrafficUsed != 360 || inbounds[0].Clients != 3 {
			t.Fatalf("inbound book totals=%+v", inbounds)
		}
		dashboard, err := svc.Dashboard(u)
		if err != nil {
			t.Fatal(err)
		}
		if dashboard.Active != 0 || len(dashboard.Attention) != 3 {
			t.Fatalf("canonical attention active=%d attention=%+v", dashboard.Active, dashboard.Attention)
		}
		for _, row := range dashboard.Attention {
			if row.TrafficUsed != 120 {
				t.Fatalf("attention usage=%d", row.TrafficUsed)
			}
		}
	})
	t.Run("failed quota changes rollback allocations and retries do not repeat", func(t *testing.T) {
		u, ib := resellerReservationFixture(t, "reseller-allocation")
		input := ResellerClientInput{Email: "reseller-allocation-client", TotalGB: 100, ExpiryTime: time.Now().Add(time.Hour).UnixMilli(), Enable: true, InboundIDs: []int{ib.Id}}
		created, err := svc.CreateClient(u, input)
		if err != nil {
			t.Fatal(err)
		}
		assertResellerAllocations(t, u.Id, 100, 1)
		rejected := errors.New("injected client transaction rollback")
		callback := func(tx *gorm.DB) {
			if tx.Statement.Table == "clients" {
				tx.AddError(rejected)
			}
		}
		if err := db.Callback().Update().After("gorm:update").Register("reseller-quota-rollback", callback); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Callback().Update().Remove("reseller-quota-rollback") })
		input.TotalGB = 200
		for range 2 {
			if _, err := svc.UpdateClient(u, created.ClientID, input); !errors.Is(err, rejected) {
				t.Fatalf("update failure=%v", err)
			}
			assertResellerAllocations(t, u.Id, 100, 1)
		}
		if err := db.Callback().Update().Remove("reseller-quota-rollback"); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if _, err := svc.UpdateClient(u, created.ClientID, input); err != nil {
				t.Fatal(err)
			}
			assertResellerAllocations(t, u.Id, 200, 2)
		}
		if err := db.Model(&core.ClientTraffic{}).Where("email = ?", input.Email).Update("up", 30).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Callback().Update().After("gorm:update").Register("reseller-reset-rollback", func(tx *gorm.DB) {
			if tx.Statement.Table == "client_traffics" {
				tx.AddError(rejected)
			}
		}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Callback().Update().Remove("reseller-reset-rollback") })
		if err := svc.ResetClient(u, created.ClientID); !errors.Is(err, rejected) {
			t.Fatalf("reset failure=%v", err)
		}
		assertResellerAllocations(t, u.Id, 200, 2)
		if err := db.Callback().Update().Remove("reseller-reset-rollback"); err != nil {
			t.Fatal(err)
		}
		for range 2 {
			if err := svc.ResetClient(u, created.ClientID); err != nil {
				t.Fatal(err)
			}
			assertResellerAllocations(t, u.Id, 200, 2)
		}
		var account model.ResellerAccount
		if err := db.First(&account, "user_id = ?", u.Id).Error; err != nil {
			t.Fatal(err)
		}
		if account.TrafficUsed != 30 {
			t.Fatalf("reset changed lifetime usage: %d", account.TrafficUsed)
		}
	})
	t.Run("failed provisioning retains one durable reservation and recovers", func(t *testing.T) {
		u, ib := resellerReservationFixture(t, "reseller-reservation")
		rejected := errors.New("injected inbound provisioning rollback")
		if err := db.Callback().Update().After("gorm:update").Register("reseller-provision-rollback", func(tx *gorm.DB) {
			if tx.Statement.Table == "inbounds" {
				tx.AddError(rejected)
			}
		}); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = db.Callback().Update().Remove("reseller-provision-rollback") })
		input := ResellerClientInput{Email: "reseller-reserved-client", TotalGB: 100, ExpiryTime: time.Now().Add(time.Hour).UnixMilli(), Enable: true, InboundIDs: []int{ib.Id}}
		reserved, err := svc.CreateClient(u, input)
		if !errors.Is(err, rejected) || reserved == nil || reserved.ClientID <= 0 {
			t.Fatalf("reservation=%+v err=%v", reserved, err)
		}
		assertResellerAllocations(t, u.Id, 100, 1)
		if _, err := svc.CreateClient(u, input); err == nil {
			t.Fatal("same identity was reserved twice")
		}
		assertResellerAllocations(t, u.Id, 100, 1)
		if err := db.Callback().Update().Remove("reseller-provision-rollback"); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.UpdateClient(u, reserved.ClientID, input); err != nil {
			t.Fatal(err)
		}
		assertResellerAllocations(t, u.Id, 100, 1)
		row, err := svc.OwnedClient(u, reserved.ClientID)
		if err != nil {
			t.Fatal(err)
		}
		ids, err := (&ClientService{}).GetInboundIdsForRecord(row.Id)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(ids, []int{ib.Id}) {
			t.Fatalf("reservation did not provision: %v", ids)
		}
	})
}
