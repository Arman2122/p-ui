package service

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestFleetResetCadenceKeepsMonthlyAnchorAndSkipsMissedSlots(t *testing.T) {
	anchor := time.Date(2028, time.January, 31, 9, 30, 0, 0, time.UTC)
	for _, test := range []struct {
		name, cycle string
		after, want time.Time
	}{
		{"leap clamp", "monthly", anchor, time.Date(2028, time.February, 29, 9, 30, 0, 0, time.UTC)},
		{"restore month day", "monthly", time.Date(2028, time.February, 29, 9, 30, 0, 0, time.UTC), time.Date(2028, time.March, 31, 9, 30, 0, 0, time.UTC)},
		{"missed months", "monthly", time.Date(2028, time.May, 7, 12, 0, 0, 0, time.UTC), time.Date(2028, time.May, 31, 9, 30, 0, 0, time.UTC)},
		{"missed days", "daily", anchor.Add(73 * time.Hour), anchor.Add(96 * time.Hour)},
		{"missed weeks", "weekly", anchor.Add(22 * 24 * time.Hour), anchor.Add(28 * 24 * time.Hour)},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := nextFleetResetDue(test.cycle, anchor.UnixMilli(), test.after.UnixMilli())
			if err != nil || got != test.want.UnixMilli() {
				t.Fatalf("anchored reset=%s err=%v want=%s", time.UnixMilli(got), err, test.want)
			}
		})
	}
}

func TestFleetResetScheduleConfigurationIsIdempotentAndDoesNotResetAllowancePostgres(t *testing.T) {
	f := fleetFixture(t, false)
	placement := f.approve(t, f.materialize(t, 0))
	active := f.activate(t, placement)
	if err := f.db.Model(&model.ClientRecord{}).Where("id = ?", f.client.Id).Update("expiry_time", 0).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.FleetTerm{}).Where("id = ?", active.Term.ID).Update("expires_unix_milli", 0).Error; err != nil {
		t.Fatal(err)
	}
	now := time.Date(2028, time.January, 31, 9, 30, 0, 0, time.UTC)
	previousClock := fleetResetNow
	fleetResetNow = func() time.Time { return now }
	t.Cleanup(func() { fleetResetNow = previousClock })
	var budget model.FleetBudget
	if err := f.db.First(&budget, "id = ?", active.Term.BudgetID).Error; err != nil {
		t.Fatal(err)
	}
	request := FleetResetScheduleRequest{OperationID: uuid.NewString(), ClientID: placement.ClientID, Override: true, Cycle: "monthly"}
	first, err := configureFleetResetSchedule(t.Context(), f.db, &f.admin, request)
	want := time.Date(2028, time.February, 29, 9, 30, 0, 0, time.UTC).UnixMilli()
	if err != nil || first.Revision != 1 || first.NextDueUnixMilli != want || first.Inherited || first.PendingOperationID != "" {
		t.Fatalf("configured schedule=%+v err=%v", first, err)
	}
	now = now.Add(24 * time.Hour)
	unchanged := request
	unchanged.OperationID, unchanged.ExpectedRevision = uuid.NewString(), 1
	same, err := configureFleetResetSchedule(t.Context(), f.db, &f.admin, unchanged)
	if err != nil || same != first {
		t.Fatalf("saving identical policy moved reset=%+v err=%v", same, err)
	}
	changed := unchanged
	changed.OperationID, changed.Cycle = uuid.NewString(), "daily"
	updated, err := configureFleetResetSchedule(t.Context(), f.db, &f.admin, changed)
	if err != nil || updated.Revision != 2 || updated.NextDueUnixMilli != now.Add(24*time.Hour).UnixMilli() {
		t.Fatalf("changed policy=%+v err=%v", updated, err)
	}
	replayed, err := configureFleetResetSchedule(t.Context(), f.db, &f.admin, request)
	if err != nil || !replayed.Replayed || replayed.Revision != first.Revision || replayed.NextDueUnixMilli != first.NextDueUnixMilli {
		t.Fatalf("lost-response replay=%+v err=%v", replayed, err)
	}
	var saved model.FleetResetSchedule
	if err := f.db.First(&saved, "client_id = ?", placement.ClientID).Error; err != nil || saved.Revision != 2 || saved.EffectiveCycle != "daily" {
		t.Fatalf("historical replay overwrote current schedule=%+v err=%v", saved, err)
	}
	var after model.FleetBudget
	var terms int64
	if err := f.db.First(&after, "id = ?", budget.ID).Error; err != nil || after.UsedBytes != budget.UsedBytes || after.ReservedBytes != budget.ReservedBytes || after.LimitBytes != budget.LimitBytes {
		t.Fatalf("schedule configuration changed allowance=%+v err=%v", after, err)
	}
	if err := f.db.Model(&model.FleetTerm{}).Where("client_id = ?", placement.ClientID).Count(&terms).Error; err != nil || terms != 1 {
		t.Fatalf("schedule configuration created billing terms=%d err=%v", terms, err)
	}
	stale := unchanged
	stale.OperationID = uuid.NewString()
	if _, err := configureFleetResetSchedule(t.Context(), f.db, &f.admin, stale); !errors.Is(err, controlplane.ErrRevision) {
		t.Fatalf("stale schedule revision=%v", err)
	}
}

func TestFleetResetScheduleRejectsPendingTransitionAndRollsBackReceiptPostgres(t *testing.T) {
	f := fleetFixture(t, false)
	placement := f.approve(t, f.materialize(t, 0))
	f.activate(t, placement)
	request := FleetResetScheduleRequest{OperationID: uuid.NewString(), ClientID: placement.ClientID, Override: true, Cycle: "daily"}
	injected := errors.New("injected reset schedule storage failure")
	callback := "test:reset_schedule_storage"
	if err := f.db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "fleet_reset_schedules" {
			tx.AddError(injected)
		}
	}); err != nil {
		t.Fatal(err)
	}
	_, err := configureFleetResetSchedule(t.Context(), f.db, &f.admin, request)
	_ = f.db.Callback().Create().Remove(callback)
	if !errors.Is(err, injected) {
		t.Fatalf("storage error=%v", err)
	}
	var receipts int64
	if err := f.db.Model(&model.FleetOperation{}).Where("id = ?", request.OperationID).Count(&receipts).Error; err != nil || receipts != 0 {
		t.Fatalf("failed schedule left operation receipt=%d err=%v", receipts, err)
	}
	if _, err := configureFleetResetSchedule(t.Context(), f.db, &f.admin, request); err != nil {
		t.Fatal(err)
	}
	pending, err := (FleetClientService{}).Transition(t.Context(), &f.admin, FleetTransitionRequest{
		OperationID: uuid.NewString(), PlacementID: placement.ID, Kind: controlplane.TrafficReset,
		ExpectedPlacementRevision: uint64(placement.Revision), ExpectedCredentialGeneration: uint64(placement.CredentialGeneration),
	})
	if err != nil || !pending.PendingDrain {
		t.Fatalf("pending reset=%+v err=%v", pending, err)
	}
	request.OperationID, request.ExpectedRevision, request.Cycle = uuid.NewString(), 1, "weekly"
	if _, err := configureFleetResetSchedule(t.Context(), f.db, &f.admin, request); !errors.Is(err, controlplane.ErrDrain) {
		t.Fatalf("schedule edit erased pending transition=%v", err)
	}
	var saved model.FleetResetSchedule
	if err := f.db.First(&saved, "client_id = ?", placement.ClientID).Error; err != nil || saved.Revision != 1 || saved.EffectiveCycle != "daily" {
		t.Fatalf("pending reset changed schedule=%+v err=%v", saved, err)
	}
}

func TestFleetResetScheduleInheritsOneAppliedPlanAcrossDifferentInboundsPostgres(t *testing.T) {
	f := fleetFixture(t, false)
	plan := model.Policy{Name: "shared reset", Rev: 1, ResetCycle: "weekly", OnDepletion: "disable", Tiers: `[{"fromBytes":0,"upBps":0,"downBps":0}]`}
	if err := f.db.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&model.ClientPolicy{Email: f.client.Email, PolicyId: &plan.Id, AppliedRev: 1, AppliedOnDepletion: "disable", AppliedResetCycle: "weekly"}).Error; err != nil {
		t.Fatal(err)
	}
	for index, cycle := range []string{"daily", "monthly"} {
		if err := f.db.Model(&f.inbounds[index]).Update("traffic_reset", cycle).Error; err != nil {
			t.Fatal(err)
		}
	}
	placement := f.approve(t, f.materialize(t, 0))
	f.approve(t, f.materialize(t, 1))
	active := f.activate(t, placement)
	schedule := readFleetResetSchedule(t, f.db, placement.ClientID)
	if schedule.OverrideCycle != nil || schedule.EffectiveCycle != "weekly" || schedule.AnchorUnixMilli != active.Term.StartedUnixMilli || schedule.NextDueUnixMilli != active.Term.StartedUnixMilli+7*24*60*60*1000 {
		t.Fatalf("inbounds replaced shared applied-plan cadence=%+v", schedule)
	}
	request := FleetResetScheduleRequest{OperationID: uuid.NewString(), ClientID: placement.ClientID, ExpectedRevision: 1, Override: true, Cycle: "monthly"}
	view, err := configureFleetResetSchedule(t.Context(), f.db, &f.admin, request)
	if err != nil || view.Inherited || view.Cycle != "monthly" || view.Revision != 2 {
		t.Fatalf("explicit client cadence did not override plan=%+v err=%v", view, err)
	}
	request.OperationID, request.ExpectedRevision, request.Override, request.Cycle = uuid.NewString(), 2, false, ""
	view, err = configureFleetResetSchedule(t.Context(), f.db, &f.admin, request)
	if err != nil || !view.Inherited || view.Cycle != "weekly" || view.Revision != 3 {
		t.Fatalf("client did not return to applied-plan cadence=%+v err=%v", view, err)
	}
}
