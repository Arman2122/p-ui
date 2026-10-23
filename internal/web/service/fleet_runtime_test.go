package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"testing"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	webruntime "github.com/Arman2122/p-ui/internal/web/runtime"
)

func fleetRuntimeEnableFixture(t *testing.T, db *gorm.DB) webruntime.SourceEnableRuntime {
	t.Helper()
	dispatch, err := NewFleetRuntime(db)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := webruntime.NewFleetManager(dispatch)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := manager.RuntimeFor(nil)
	if err != nil {
		t.Fatal(err)
	}
	enable, ok := selected.(webruntime.SourceEnableRuntime)
	if !ok {
		t.Fatal("fleet dispatcher lacks source enable capability")
	}
	return enable
}

func assertFleetRuntimeUnchanged(t *testing.T, db *gorm.DB, clientID int, request FleetClientEnableRequest) {
	t.Helper()
	var source model.ClientRecord
	var owner model.FleetClient
	var operations, work int64
	if err := db.First(&source, clientID).Error; err != nil || !source.Enable {
		t.Fatalf("rejected runtime command changed source=%+v err=%v", source, err)
	}
	if err := db.First(&owner, "id = ?", request.ClientID).Error; err != nil || uint64(owner.Revision) != request.ExpectedRevision {
		t.Fatalf("rejected runtime command changed owner=%+v err=%v", owner, err)
	}
	if err := db.Model(&model.FleetOperation{}).Where("id = ?", request.OperationID).Count(&operations).Error; err != nil || operations != 0 {
		t.Fatalf("rejected runtime command committed receipt=%d err=%v", operations, err)
	}
	if err := db.Model(&model.FleetSourceWork{}).Where("client_id = ?", request.ClientID).Count(&work).Error; err != nil || work != 0 {
		t.Fatalf("rejected runtime command committed source work=%d err=%v", work, err)
	}
}

func TestFleetRuntimeRejectsLegacyCommands(t *testing.T) {
	var dispatch webruntime.Runtime = &fleetRuntime{}
	ctx, inbound := t.Context(), &model.Inbound{Id: 7}
	commands := []struct {
		name string
		call func() error
	}{
		{"AddInbound", func() error { return dispatch.AddInbound(ctx, inbound) }},
		{"DelInbound", func() error { return dispatch.DelInbound(ctx, inbound) }},
		{"UpdateInbound", func() error { return dispatch.UpdateInbound(ctx, inbound, inbound) }},
		{"AddUser", func() error { return dispatch.AddUser(ctx, inbound, "client") }},
		{"RemoveUser", func() error { return dispatch.RemoveUser(ctx, inbound, "client") }},
		{"UpdateUser", func() error { return dispatch.UpdateUser(ctx, inbound, "client", model.Client{Enable: false}) }},
		{"DeleteUser", func() error { return dispatch.DeleteUser(ctx, inbound, "client") }},
		{"AddClient", func() error { return dispatch.AddClient(ctx, inbound, model.Client{}) }},
		{"DeleteClient", func() error { return dispatch.DeleteClient(ctx, "client") }},
		{"RestartXray", func() error { return dispatch.RestartXray(ctx) }},
		{"ResetClientTraffic", func() error { return dispatch.ResetClientTraffic(ctx, inbound, "client") }},
		{"ResetInboundTraffic", func() error { return dispatch.ResetInboundTraffic(ctx, inbound) }},
		{"ResetAllTraffics", func() error { return dispatch.ResetAllTraffics(ctx) }},
		{"CutSessions", func() error {
			count, err := dispatch.CutSessions(ctx, inbound, "client", netip.MustParseAddr("192.0.2.1"))
			if count != 0 {
				t.Fatalf("unsupported session operation claimed %d cuts", count)
			}
			return err
		}},
	}
	for _, command := range commands {
		t.Run(command.name, func(t *testing.T) {
			if err := command.call(); !errors.Is(err, ErrFleetRuntimeCommand) {
				t.Fatalf("legacy command returned %v", err)
			}
		})
	}
}

func TestFleetRuntimeEnablePreservesTransactionAndAuthorityPostgres(t *testing.T) {
	f := newAuthorityFixture(t)
	enable := fleetRuntimeEnableFixture(t, f.f.db)
	request := sourceEnableRequest(t, f.f, f.materialized.Client.ID, false)
	if _, err := enable.SetClientEnabledTx(t.Context(), f.f.db, &f.f.admin, request); !errors.Is(err, ErrFleetRuntimeDatabase) {
		t.Fatalf("nontransactional source command=%v", err)
	}
	fault := errors.New("caller transaction rejected")
	err := f.f.db.Transaction(func(tx *gorm.DB) error {
		result, err := enable.SetClientEnabledTx(t.Context(), tx, &f.f.admin, request)
		if err != nil || !result.Pending || result.Enabled || result.Targets != 1 {
			t.Fatalf("runtime source command=%+v err=%v", result, err)
		}
		return fault
	})
	if !errors.Is(err, fault) {
		t.Fatal(err)
	}
	assertFleetRuntimeUnchanged(t, f.f.db, f.f.client.Id, request)
	staleActor := f.f.admin
	staleActor.LoginEpoch++
	err = f.f.db.Transaction(func(tx *gorm.DB) error {
		_, err := enable.SetClientEnabledTx(t.Context(), tx, &staleActor, request)
		return err
	})
	if !errors.Is(err, ErrResellerForbidden) {
		t.Fatalf("runtime accepted stale actor=%v", err)
	}
	assertFleetRuntimeUnchanged(t, f.f.db, f.f.client.Id, request)
	for _, replay := range []bool{false, true} {
		err = f.f.db.Transaction(func(tx *gorm.DB) error {
			result, err := enable.SetClientEnabledTx(t.Context(), tx, &f.f.admin, request)
			if err == nil && (result.Replayed != replay || !result.Pending || result.Enabled || result.Revision != request.ExpectedRevision+1) {
				t.Fatalf("runtime enable receipt=%+v replay=%v", result, replay)
			}
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	conflict := request
	conflict.Enabled = true
	err = f.f.db.Transaction(func(tx *gorm.DB) error {
		_, err := enable.SetClientEnabledTx(t.Context(), tx, &f.f.admin, conflict)
		return err
	})
	if !errors.Is(err, controlplane.ErrConflict) {
		t.Fatalf("runtime changed replay payload=%v", err)
	}
	var grant model.FleetGrant
	var share model.FleetRateShare
	var work model.FleetSourceWork
	if err := f.f.db.First(&grant, "id = ?", f.credit.Grant.ID).Error; err != nil || grant.IssuedBytes != f.credit.Grant.IssuedBytes || grant.Revision != f.credit.Grant.Revision || grant.TermID != f.active.Term.ID {
		t.Fatalf("runtime source edit changed credit=%+v err=%v", grant, err)
	}
	if err := f.f.db.First(&share, "id = ?", grant.ID).Error; err != nil || share.Revision != f.credit.Rates.Revision {
		t.Fatalf("runtime source edit released rate=%+v err=%v", share, err)
	}
	var before, after controlplane.Rates
	if json.Unmarshal([]byte(f.credit.Rates.Outstanding), &before) != nil || json.Unmarshal([]byte(share.Outstanding), &after) != nil || before != after {
		t.Fatalf("runtime source edit changed outstanding rates=%+v want=%+v", after, before)
	}
	if err := f.f.db.First(&work, "placement_id = ?", f.materialized.Placement.ID).Error; err != nil || work.Revision != 1 || !work.Pending || !work.RequiresStop {
		t.Fatalf("runtime source edit lost durable stop=%+v err=%v", work, err)
	}
}

func TestFleetRuntimeRejectsForeignAndClosedPoolsPostgres(t *testing.T) {
	f := fleetFixture(t, false)
	materialized := f.materialize(t, 0)
	f.approve(t, materialized)
	enable := fleetRuntimeEnableFixture(t, f.db)
	request := sourceEnableRequest(t, f, materialized.Client.ID, false)
	foreign, err := gorm.Open(postgres.Open(os.Getenv("PUI_DB_DSN")), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	pool, err := foreign.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	err = foreign.Transaction(func(tx *gorm.DB) error {
		_, err := enable.SetClientEnabledTx(t.Context(), tx, &f.admin, request)
		return err
	})
	if !errors.Is(err, ErrFleetRuntimeDatabase) {
		t.Fatalf("foreign pool transaction accepted=%v", err)
	}
	if _, err := NewFleetRuntime(foreign); !errors.Is(err, ErrFleetRuntimeDatabase) {
		t.Fatalf("foreign constructor accepted=%v", err)
	}
	assertFleetRuntimeUnchanged(t, f.db, f.client.Id, request)
	if err := database.CloseDB(); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFleetRuntime(f.db); !errors.Is(err, ErrFleetRuntimeDatabase) {
		t.Fatalf("closed constructor accepted=%v", err)
	}
	err = f.db.Transaction(func(tx *gorm.DB) error {
		_, err := enable.SetClientEnabledTx(t.Context(), tx, &f.admin, request)
		return err
	})
	if err == nil || err.Error() != "sql: database is closed" {
		t.Fatalf("closed pool mutation accepted=%v", err)
	}
}

func TestFleetRuntimeReplacementDuringCommandRollsBackPostgres(t *testing.T) {
	f := fleetFixture(t, false)
	materialized := f.materialize(t, 0)
	f.approve(t, materialized)
	enable := fleetRuntimeEnableFixture(t, f.db)
	request := sourceEnableRequest(t, f, materialized.Client.ID, false)
	callback := "fleet-runtime-replace-" + uuid.NewString()
	replaced := false
	var replacementErr error
	if err := f.db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		if replaced || tx.Statement.Table != "users" {
			return
		}
		replaced = true
		replacementErr = database.InitDB()
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.db.Callback().Query().Remove(callback) })
	err := f.db.Transaction(func(tx *gorm.DB) error {
		_, err := enable.SetClientEnabledTx(t.Context(), tx, &f.admin, request)
		return err
	})
	if !replaced || replacementErr != nil || !errors.Is(err, ErrFleetRuntimeDatabase) {
		t.Fatalf("replacement during command replaced=%v replacement=%v command=%v", replaced, replacementErr, err)
	}
	current := database.GetDB()
	if current == f.db {
		t.Fatal("database lifecycle did not replace the real handle")
	}
	assertFleetRuntimeUnchanged(t, current, f.client.Id, request)
	err = current.Transaction(func(tx *gorm.DB) error {
		_, err := enable.SetClientEnabledTx(context.Background(), tx, &f.admin, request)
		return err
	})
	if !errors.Is(err, ErrFleetRuntimeDatabase) {
		t.Fatalf("stale dispatcher rebound to replacement=%v", err)
	}
}
