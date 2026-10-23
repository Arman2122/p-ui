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
)

type failingQuotaCore struct {
	*proxyCore
	failures    map[string]error
	calls       map[string]int
	checkWriter func() error
	addFailure  error
}

func (c *failingQuotaCore) AddUser(context.Context, core.Instance, core.User) error {
	return c.addFailure
}

func (c *failingQuotaCore) ResetQuota(_ context.Context, email string) error {
	c.calls[email]++
	if c.checkWriter != nil {
		if err := c.checkWriter(); err != nil {
			return err
		}
	}
	return c.failures[email]
}

func TestNativeQuotaFailureAndRetry(t *testing.T) {
	initTestDB(t)
	t.Setenv("PUI_LOG_FOLDER", t.TempDir())
	resetTrafficWriterForTest(t)
	StartTrafficWriter()
	daemonFailure := errors.New("daemon refused quota reset")
	fake := &failingQuotaCore{
		proxyCore: newProxyCore(&declaredCore{id: "native-test", kind: "vless"}),
		failures:  map[string]error{}, calls: map[string]int{},
	}
	installCores(t, fake)
	fake.checkWriter = func() error {
		done := make(chan error, 1)
		go func() { done <- submitTrafficWrite(func() error { return nil }) }()
		select {
		case err := <-done:
			return err
		case <-time.After(5 * time.Second):
			return errors.New("daemon reset held the traffic writer")
		}
	}
	db := database.GetDB()
	inbounds, clients := &InboundService{}, &ClientService{}
	seed := func(t *testing.T, email string, port int, expiry int64, reset int) *model.Inbound {
		t.Helper()
		c := model.Client{Email: email, ID: "11111111-1111-1111-1111-111111111111", Enable: true, ExpiryTime: expiry, Reset: reset}
		ib := &model.Inbound{
			UserId: 1, Tag: email, Port: port, Protocol: model.VLESS, Enable: true,
			Settings: clientsSettings(t, []model.Client{c}), LastTrafficResetTime: 17,
		}
		if err := db.Create(ib).Error; err != nil {
			t.Fatal(err)
		}
		if err := clients.SyncInbound(db, ib.Id, []model.Client{c}); err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&core.ClientTraffic{Email: email, InboundId: ib.Id, Enable: true, Up: 110, Down: 220, ExpiryTime: expiry, Reset: reset}).Error; err != nil {
			t.Fatal(err)
		}
		fake.failures[email] = daemonFailure
		return ib
	}

	t.Run("manual and periodic resets report failure and retry", func(t *testing.T) {
		for i, tc := range []struct {
			name   string
			marked bool
			run    func(*model.Inbound, string) error
		}{
			{"email", false, func(_ *model.Inbound, email string) error { return inbounds.ResetClientTrafficByEmail(email) }},
			{"inbound", true, func(ib *model.Inbound, email string) error {
				_, err := inbounds.ResetClientTraffic(ib.Id, email)
				return err
			}},
			{"client", true, func(_ *model.Inbound, email string) error {
				_, err := clients.ResetTrafficByEmail(inbounds, email)
				return err
			}},
			{"bulk", false, func(_ *model.Inbound, email string) error {
				_, err := clients.BulkResetTraffic(inbounds, []string{email})
				return err
			}},
			{"periodic", true, func(ib *model.Inbound, _ string) error { return clients.ResetAllClientTraffics(inbounds, ib.Id) }},
			{"all clients", true, func(_ *model.Inbound, _ string) error { return clients.ResetAllClientTraffics(inbounds, -1) }},
			{"all traffic", false, func(_ *model.Inbound, _ string) error { _, err := clients.ResetAllTraffics(inbounds); return err }},
			{"hand counter", false, func(_ *model.Inbound, email string) error { return inbounds.UpdateClientTrafficByEmail(email, 0, 0) }},
		} {
			t.Run(tc.name, func(t *testing.T) {
				email := fmt.Sprintf("native-reset-%d", i)
				ib := seed(t, email, 49000+i, 0, 0)
				if err := tc.run(ib, email); !errors.Is(err, daemonFailure) || !errors.Is(err, ErrCoreQuotaReset) {
					t.Fatalf("reset failure=%v, want typed daemon failure", err)
				}
				if err := db.First(ib, ib.Id).Error; err != nil {
					t.Fatal(err)
				}
				if ib.LastTrafficResetTime != 17 {
					t.Fatalf("failed reset advanced timestamp to %d", ib.LastTrafficResetTime)
				}
				fake.failures[email] = nil
				if err := tc.run(ib, email); err != nil {
					t.Fatalf("retry: %v", err)
				}
				if fake.calls[email] != 2 {
					t.Fatalf("daemon attempts=%d, want2", fake.calls[email])
				}
				if err := db.First(ib, ib.Id).Error; err != nil {
					t.Fatal(err)
				}
				if tc.marked && ib.LastTrafficResetTime <= 17 {
					t.Fatal("successful retry did not mark reset")
				}
			})
		}
	})

	t.Run("plan cursor waits for native reset", func(t *testing.T) {
		ib := seed(t, "native-plan-reset", 49020, 0, 0)
		p := model.Policy{Name: "native weekly", Rev: 1, ResetCycle: "weekly"}
		if err := db.Create(&p).Error; err != nil {
			t.Fatal(err)
		}
		now := time.Now()
		a := model.ClientPolicy{Email: "native-plan-reset", PolicyId: &p.Id, AppliedRev: 1, AppliedResetCycle: "weekly", UpdatedAt: now.Add(-8 * 24 * time.Hour).UnixMilli()}
		if err := db.Create(&a).Error; err != nil {
			t.Fatal(err)
		}
		result, err := (&PolicyService{}).ConvergePlanCycles(context.Background(), now)
		if !errors.Is(err, daemonFailure) || result.Reset != 0 {
			t.Fatalf("plan failure result=%+v err=%v", result, err)
		}
		if err := db.Where("email = ?", a.Email).First(&a).Error; err != nil {
			t.Fatal(err)
		}
		if a.LastResetAt != 0 {
			t.Fatalf("plan cursor advanced to%d", a.LastResetAt)
		}
		fake.failures[a.Email] = nil
		result, err = (&PolicyService{}).ConvergePlanCycles(context.Background(), now)
		if err != nil || result.Reset != 1 {
			t.Fatalf("plan retry result=%+v err=%v", result, err)
		}
		if err := db.First(ib, ib.Id).Error; err != nil {
			t.Fatal(err)
		}
		if ib.LastTrafficResetTime <= 17 {
			t.Fatal("native plan retry not recorded")
		}
	})

	t.Run("restart hint survives native reset failure", func(t *testing.T) {
		ib := seed(t, "native-restart-hint", 49022, 0, 0)
		if err := db.Model(&core.ClientTraffic{}).Where("email = ?", "native-restart-hint").Update("enable", false).Error; err != nil {
			t.Fatal(err)
		}
		fake.addFailure = errors.New("core requires reconcile")
		nr, err := clients.ResetTrafficByEmail(inbounds, "native-restart-hint")
		fake.addFailure = nil
		if !nr || !errors.Is(err, daemonFailure) {
			t.Fatalf("restart hint lost: needRestart=%v err=%v", nr, err)
		}
		fake.failures["native-restart-hint"] = nil
		if _, err := clients.ResetTrafficByEmail(inbounds, "native-restart-hint"); err != nil {
			t.Fatal(err)
		}
		if err := db.First(ib, ib.Id).Error; err != nil {
			t.Fatal(err)
		}
		if ib.LastTrafficResetTime <= 17 {
			t.Fatal("retry did not mark the completed reset")
		}
	})

	t.Run("automatic renewal waits for native reset", func(t *testing.T) {
		past := time.Now().Add(-24 * time.Hour).UnixMilli()
		ib := seed(t, "native-auto-renew", 49021, past, 30)
		if err := db.Create(&model.ClientHistoryCursor{Email: "native-auto-renew"}).Error; err != nil {
			t.Fatal(err)
		}
		if err := RecordClientHistory(time.Now()); err != nil {
			t.Fatal(err)
		}
		pushGlobal := func() {
			t.Helper()
			if err := db.Create(&model.ClientGlobalTraffic{MasterGuid: "reset-master", Email: "native-auto-renew", Up: 10000, Down: 30000, UpdatedAt: time.Now().UnixMilli()}).Error; err != nil {
				t.Fatal(err)
			}
		}
		pushGlobal()
		_, _, _, err := inbounds.AddTrafficDetailed(nil, []*core.ClientTraffic{{Email: "native-auto-renew", Up: 20, Down: 30}})
		if !errors.Is(err, daemonFailure) {
			t.Fatalf("auto-renew failure=%v", err)
		}
		var rec model.ClientRecord
		if err := db.Where("email = ?", "native-auto-renew").First(&rec).Error; err != nil {
			t.Fatal(err)
		}
		if rec.ExpiryTime != past {
			t.Fatalf("failed renewal expiry=%d, want%d", rec.ExpiryTime, past)
		}
		var sample model.ClientTrafficSample
		if err := db.Model(&model.ClientTrafficSample{}).Select("SUM(up) AS up, SUM(down) AS down").Where("email = ?", rec.Email).Scan(&sample).Error; err != nil {
			t.Fatal(err)
		}
		if sample.Up != 130 || sample.Down != 250 {
			t.Fatalf("reset lost current poll: history=%d/%d, want130/250", sample.Up, sample.Down)
		}
		pushGlobal()
		if err := RecordClientHistory(time.Now()); err != nil {
			t.Fatal(err)
		}
		fake.failures[rec.Email] = nil
		if _, _, _, err := inbounds.AddTrafficDetailed(nil, nil); err != nil {
			t.Fatalf("automatic retry: %v", err)
		}
		if err := db.First(&rec, rec.Id).Error; err != nil {
			t.Fatal(err)
		}
		if want := past + 30*86400000; rec.ExpiryTime != want || !rec.Enable {
			t.Fatalf("renewed client=%+v, want expiry%d and enabled", rec, want)
		}
		if fake.calls[rec.Email] != 2 {
			t.Fatalf("automatic reset attempts=%d, want2", fake.calls[rec.Email])
		}
		if err := db.First(ib, ib.Id).Error; err != nil {
			t.Fatal(err)
		}
		if ib.LastTrafficResetTime <= 17 {
			t.Fatal("automatic retry not recorded")
		}
		if err := RecordClientHistory(time.Now()); err != nil {
			t.Fatal(err)
		}
		if err := db.Model(&model.ClientTrafficSample{}).Select("SUM(up) AS up, SUM(down) AS down").Where("email = ?", rec.Email).Scan(&sample).Error; err != nil {
			t.Fatal(err)
		}
		if sample.Up != 130 || sample.Down != 250 {
			t.Fatalf("fresh global snapshot replayed after failed reset/retry: history=%d/%d, want130/250", sample.Up, sample.Down)
		}
	})
}
