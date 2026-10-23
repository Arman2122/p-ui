package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"

	"gorm.io/gorm"
)

func requireRenewalCount(t *testing.T, email string, want int64) {
	t.Helper()
	row := readTraffic(t, database.GetDB(), email)
	if row.ResetCount == nil {
		t.Fatalf("renewal count for %s = nil, want %d", email, want)
	}
	if *row.ResetCount != want {
		t.Fatalf("renewal count for %s = %d, want %d", email, *row.ResetCount, want)
	}
}

func TestXrayRenewalGenerationCommitsOnceAcrossInbounds(t *testing.T) {
	setupBulkDB(t)
	ib := seedRenewableNeighbour(t, 23211, nil)
	clients, err := (&InboundService{}).GetClients(ib)
	if err != nil {
		t.Fatal(err)
	}
	second := mkInbound(t, 23212, model.VLESS, clientsSettings(t, clients))
	if err := (&ClientService{}).SyncInbound(nil, second.Id, clients); err != nil {
		t.Fatal(err)
	}
	rollback := errors.New("rollback renewal")
	err = database.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := autoRenewTick(tx); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("rollback error = %v", err)
	}
	requireRenewalCount(t, "y@stale", 0)
	if err := database.GetDB().Transaction(autoRenewTick); err != nil {
		t.Fatal(err)
	}
	requireRenewalCount(t, "y@stale", 1)
	for _, id := range []int{ib.Id, second.Id} {
		requireNeighbourRenewed(t, id)
	}
	if err := database.GetDB().Transaction(autoRenewTick); err != nil {
		t.Fatal(err)
	}
	if _, err := (&ClientService{}).ResetTrafficByEmail(&InboundService{}, "y@stale"); err != nil {
		t.Fatal(err)
	}
	requireRenewalCount(t, "y@stale", 1)
}

func TestXrayRenewalGenerationOverflowRollsBackExpiry(t *testing.T) {
	setupBulkDB(t)
	ib := seedRenewableNeighbour(t, 23213, nil)
	before := readTraffic(t, database.GetDB(), "y@stale")
	if err := database.GetDB().Model(&core.ClientTraffic{}).Where("email = ?", before.Email).Update("reset_count", int64(math.MaxInt64)).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.GetDB().Transaction(autoRenewTick); err == nil || err.Error() != "client renewal count overflow" {
		t.Fatalf("overflow error = %v", err)
	}
	after := readTraffic(t, database.GetDB(), before.Email)
	if after.ExpiryTime != before.ExpiryTime {
		t.Fatalf("overflow advanced expiry to %d from %d", after.ExpiryTime, before.ExpiryTime)
	}
	client, _ := settingsClient(t, ib.Id, before.Email)
	if client.ExpiryTime != before.ExpiryTime {
		t.Fatalf("overflow advanced inbound expiry to %d", client.ExpiryTime)
	}
}

func TestNativeRenewalGenerationCountsOnlyCommittedPeriods(t *testing.T) {
	setupBulkDB(t)
	failure := errors.New("native reset unavailable")
	fake := &failingQuotaCore{proxyCore: newProxyCore(&declaredCore{id: "renewal-generation", kind: "vless"}), failures: map[string]error{}, calls: map[string]int{}}
	installCores(t, fake)
	const email = "native-generation@x"
	past := time.Now().Add(-31 * 24 * time.Hour).UnixMilli()
	client := model.Client{Email: email, ID: "11111111-1111-1111-1111-111111111111", Enable: true, ExpiryTime: past, Reset: 30}
	first := 0
	for i := range 2 {
		ib := mkInbound(t, 23214+i, model.VLESS, clientsSettings(t, []model.Client{client}))
		if err := (&ClientService{}).SyncInbound(nil, ib.Id, []model.Client{client}); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = ib.Id
		}
	}
	if err := database.GetDB().Create(&core.ClientTraffic{Email: email, InboundId: first, Enable: true, ExpiryTime: past, Reset: 30}).Error; err != nil {
		t.Fatal(err)
	}
	svc := &InboundService{}
	fake.failures[email] = failure
	if _, err := svc.renewNativeClients(context.Background(), []string{email}); !errors.Is(err, failure) {
		t.Fatalf("reset failure = %v", err)
	}
	requireRenewalCount(t, email, 0)
	fake.failures[email] = nil
	if _, err := svc.renewNativeClients(context.Background(), []string{email}); err != nil {
		t.Fatal(err)
	}
	requireRenewalCount(t, email, 2)
	if _, err := svc.renewNativeClients(context.Background(), []string{email}); err != nil {
		t.Fatal(err)
	}
	if _, err := (&ClientService{}).ResetTrafficByEmail(svc, email); err != nil {
		t.Fatal(err)
	}
	requireRenewalCount(t, email, 2)
}

func TestNativeRenewalDoesNotOverwriteConcurrentExpiry(t *testing.T) {
	setupBulkDB(t)
	seedRenewableNeighbour(t, 23216, nil)
	traffic := readTraffic(t, database.GetDB(), "y@stale")
	next := traffic.ExpiryTime + 30*86400000
	manual := next + 12345
	if err := database.GetDB().Model(&core.ClientTraffic{}).Where("email = ?", traffic.Email).Update("expiry_time", manual).Error; err != nil {
		t.Fatal(err)
	}
	svc := &InboundService{renewal: &clientRenewal{email: traffic.Email, from: traffic.ExpiryTime, to: next, previousCount: 0, nextCount: 1, reset: 30}}
	err := database.GetDB().Transaction(func(tx *gorm.DB) error {
		return svc.UpdateClientStat(tx, traffic.Email, &model.Client{Email: traffic.Email, Enable: true, ExpiryTime: next, Reset: 30})
	})
	if err == nil || err.Error() != "client renewal superseded by a concurrent lifecycle change" {
		t.Fatalf("superseded renewal error = %v", err)
	}
	if got := readTraffic(t, database.GetDB(), traffic.Email); got.ExpiryTime != manual {
		t.Fatalf("concurrent expiry overwritten with %d, want %d", got.ExpiryTime, manual)
	}
	requireRenewalCount(t, traffic.Email, 0)
}

func TestNativeRenewalDoesNotOverwriteConcurrentResetPolicy(t *testing.T) {
	setupBulkDB(t)
	seedRenewableNeighbour(t, 23217, nil)
	traffic := readTraffic(t, database.GetDB(), "y@stale")
	next := traffic.ExpiryTime + 30*86400000
	if err := database.GetDB().Model(&core.ClientTraffic{}).Where("email = ?", traffic.Email).Update("reset", 0).Error; err != nil {
		t.Fatal(err)
	}
	svc := &InboundService{renewal: &clientRenewal{email: traffic.Email, from: traffic.ExpiryTime, to: next, previousCount: 0, nextCount: 1, reset: 30}}
	err := database.GetDB().Transaction(func(tx *gorm.DB) error {
		return svc.UpdateClientStat(tx, traffic.Email, &model.Client{Email: traffic.Email, Enable: true, ExpiryTime: next, Reset: 30})
	})
	if !errors.Is(err, errRenewalSuperseded) {
		t.Fatalf("superseded renewal error = %v", err)
	}
	if got := readTraffic(t, database.GetDB(), traffic.Email); got.Reset != 0 || got.ExpiryTime != traffic.ExpiryTime {
		t.Fatalf("concurrent reset policy overwritten: %+v", got)
	}
	requireRenewalCount(t, traffic.Email, 0)
}

func TestAutoRenewSkipsClientsMissingUsableSettings(t *testing.T) {
	for _, settings := range []string{`{"clients":[]}`, `{"clients":[null]}`, `{"clients":[{"email":"y@stale","expiryTime":"broken"}]}`, `{`} {
		t.Run(settings, func(t *testing.T) {
			setupBulkDB(t)
			db := database.GetDB()
			ib := seedRenewableNeighbour(t, 23218, nil)
			if err := db.Model(ib).Update("settings", settings).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&model.ClientGlobalTraffic{MasterGuid: "upstream", Email: "y@stale", Up: 1000, Down: 2000, UpdatedAt: time.Now().UnixMilli()}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&model.ClientGroup{Name: "preserve-group", ResetUp: 70, ResetDown: 80}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&model.ClientRecord{}).Where("email = ?", "y@stale").Update("group_name", "preserve-group").Error; err != nil {
				t.Fatal(err)
			}
			err := db.Transaction(func(tx *gorm.DB) error {
				_, count, emails, err := (&InboundService{}).autoRenewClients(tx)
				if err == nil && (count != 0 || len(emails) != 0) {
					return fmt.Errorf("renewed %d clients: %v", count, emails)
				}
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			traffic := readTraffic(t, db, "y@stale")
			if traffic.Up != 600 || traffic.Down != 400 || traffic.Enable {
				t.Fatalf("untouched candidate traffic changed: %+v", traffic)
			}
			var global model.ClientGlobalTraffic
			if err := db.Where("email = ?", "y@stale").First(&global).Error; err != nil || global.Up != 1000 || global.Down != 2000 {
				t.Fatalf("global traffic = %+v, error=%v", global, err)
			}
			var group model.ClientGroup
			if err := db.Where("name = ?", "preserve-group").First(&group).Error; err != nil {
				t.Fatal(err)
			}
			if group.ResetUp != 70 || group.ResetDown != 80 {
				t.Fatalf("unrenewed group baselines changed: %+v", group)
			}
			var historyRows int64
			if err := db.Model(&model.ClientHistoryCursor{}).Where("email = ?", "y@stale").Count(&historyRows).Error; err != nil {
				t.Fatal(err)
			}
			if historyRows != 0 {
				t.Fatalf("unrenewed client generated %d reset cursors", historyRows)
			}
		})
	}
}
