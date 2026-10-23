package service

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func groupedFleetFixture(t *testing.T, up, down int64) (fleetClientFixture, model.ClientGroup) {
	t.Helper()
	f := fleetFixture(t, false)
	group := model.ClientGroup{Name: "ledger-group"}
	if err := f.db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Create(&model.GroupQuota{Name: group.Name, QuotaBytes: 700}).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&f.client).Update("group_name", group.Name).Error; err != nil {
		t.Fatal(err)
	}
	f.client.Group = group.Name
	if err := f.db.Model(&core.ClientTraffic{}).Where("email = ?", f.client.Email).Updates(map[string]any{"up": up, "down": down}).Error; err != nil {
		t.Fatal(err)
	}
	return f, group
}

func groupFleetCredit(t *testing.T, f fleetClientFixture, placement model.FleetPlacement, term model.FleetTerm, bytes int64) FleetCredit {
	t.Helper()
	credit, err := (FleetClientService{}).AllocateCredit(t.Context(), FleetCreditRequest{
		OperationID: uuid.NewString(), PlacementID: placement.ID, TermID: term.ID,
		ExpectedPlacementRevision: uint64(placement.Revision), ExpectedCredentialGeneration: uint64(placement.CredentialGeneration), Bytes: bytes,
		Rates: controlplane.Rates{Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return credit
}

func TestFleetGroupUsageRetainsClosedTermsAndNeverBillsReservedAuthorityPostgres(t *testing.T) {
	f, group := groupedFleetFixture(t, 50, 70)
	a, b := f.approve(t, f.materialize(t, 0)), f.approve(t, f.materialize(t, 1))
	active := f.activate(t, a)
	var provenance model.FleetTermGroup
	if err := f.db.First(&provenance, "term_id = ?", active.Term.ID).Error; err != nil || provenance.GroupID == nil || *provenance.GroupID != group.Id {
		t.Fatalf("new activation lost immutable group provenance: %+v %v", provenance, err)
	}
	one, two := groupFleetCredit(t, f, a, active.Term, 200), groupFleetCredit(t, f, b, active.Term, 200)
	fleetUsageReport(t, f, 0, one.Grant, 1, 50, 30)
	fleetUsageReport(t, f, 1, two.Grant, 1, 20, 40)
	if err := f.db.Model(&core.ClientTraffic{}).Where("email = ?", f.client.Email).Updates(map[string]any{"up": 99000, "down": 88000}).Error; err != nil {
		t.Fatal(err)
	}
	assertUsage := func(up, down int64) {
		t.Helper()
		row := groupByName(t, &ClientService{}, group.Name)
		if row.Up != up || row.Down != down || row.TrafficUsed != up+down {
			t.Fatalf("group billed stale seed/reserved authority or lost measured usage: %+v want=%d/%d", row, up, down)
		}
		managed, err := listManagedGroupSummaries()
		if err != nil || len(managed) != 1 || managed[0].Up != up || managed[0].Down != down {
			t.Fatalf("managed group projection diverged: %+v %v", managed, err)
		}
	}
	assertUsage(120, 140)
	pending, err := (FleetClientService{}).Transition(t.Context(), &f.admin, FleetTransitionRequest{
		OperationID: uuid.NewString(), PlacementID: a.ID, Kind: controlplane.TrafficReset, ExpectedPlacementRevision: uint64(a.Revision), ExpectedCredentialGeneration: uint64(a.CredentialGeneration),
	})
	if err != nil {
		t.Fatal(err)
	}
	fleetAppliedStop(t, f, f.nodes[0], active.Term, one)
	fleetAppliedStop(t, f, f.nodes[1], active.Term, two)
	if _, err := (FleetClientService{}).FinishTransition(t.Context(), &f.admin, FleetFinishRequest{
		OperationID: uuid.NewString(), PlacementID: a.ID, PendingTermID: pending.Term.ID, ExpectedPlacementRevision: uint64(a.Revision), ExpectedCredentialGeneration: uint64(a.CredentialGeneration),
	}); err != nil {
		t.Fatal(err)
	}
	var baseline model.ClientGroup
	if err := f.db.First(&baseline, group.Id).Error; err != nil || baseline.ResetUp != group.ResetUp || baseline.ResetDown != group.ResetDown {
		t.Fatalf("owned term rollover subtracted immutable ledger history again: %+v %v", baseline, err)
	}
	assertUsage(120, 140)
	fleetUsageReport(t, f, 0, one.Grant, 2, 60, 35)
	assertUsage(130, 145)
	if err := f.db.Create(&model.ClientTrafficSample{Email: f.client.Email, T: time.Now().Unix() / 3600 * 3600, Up: 99999, Down: 99999}).Error; err != nil {
		t.Fatal(err)
	}
	details, err := (&ClientService{}).ListGroupDetails()
	if err != nil || len(details) != 1 || len(details[0].History) != 1 || details[0].History[0].Up != 80 || details[0].History[0].Down != 75 {
		t.Fatalf("group history mixed legacy samples/opening or dropped late closed-term reports: %+v %v", details, err)
	}
	if err := (&ClientService{}).ResetGroupTraffic(group.Name); !errors.Is(err, ErrFleetLifecycle) {
		t.Fatalf("display reset replenished an owned group pool: %v", err)
	}
	other := model.ClientGroup{Name: "later-source-group"}
	if err := f.db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&f.client).Update("group_name", other.Name).Error; err != nil {
		t.Fatal(err)
	}
	fleetUsageReport(t, f, 0, one.Grant, 3, 65, 35)
	old := groupByName(t, &ClientService{}, group.Name)
	current := groupByName(t, &ClientService{}, other.Name)
	if old.Up != 135 || old.Down != 145 || current.TrafficUsed != 0 {
		t.Fatalf("current membership stole historical term usage: old=%+v current=%+v", old, current)
	}
	if err := (&ClientService{}).ResetGroupTraffic(group.Name); !errors.Is(err, ErrFleetLifecycle) {
		t.Fatalf("empty current membership bypassed retained owned group reset refusal: %v", err)
	}
	if err := f.db.Where("term_id = ?", active.Term.ID).Delete(&model.FleetTermGroup{}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := (&ClientService{}).ListGroups(); !errors.Is(err, ErrFleetUsage) {
		t.Fatalf("missing historical group provenance silently became zero/current membership: %v", err)
	}
}

func TestFleetGroupFirstBudgetOpeningKeepsPriorResetOpeningPostgres(t *testing.T) {
	f, group := groupedFleetFixture(t, 50, 70)
	placement := f.approve(t, f.materialize(t, 0))
	active := f.activate(t, placement)
	pending, err := (FleetClientService{}).Transition(t.Context(), &f.admin, FleetTransitionRequest{
		OperationID: uuid.NewString(), PlacementID: placement.ID, Kind: controlplane.TrafficReset,
		ExpectedPlacementRevision: uint64(placement.Revision), ExpectedCredentialGeneration: uint64(placement.CredentialGeneration),
	})
	if err != nil {
		t.Fatal(err)
	}
	finished, err := (FleetClientService{}).FinishTransition(t.Context(), &f.admin, FleetFinishRequest{
		OperationID: uuid.NewString(), PlacementID: placement.ID, PendingTermID: pending.Term.ID,
		ExpectedPlacementRevision: uint64(placement.Revision), ExpectedCredentialGeneration: uint64(placement.CredentialGeneration),
	})
	if err != nil {
		t.Fatal(err)
	}
	var previousPin model.FleetTermGroup
	if err := f.db.First(&previousPin, "term_id = ?", active.Term.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Delete(&previousPin).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := (FleetClientService{}).AllocateCredit(t.Context(), FleetCreditRequest{
		OperationID: uuid.NewString(), PlacementID: placement.ID, TermID: finished.Term.ID,
		ExpectedPlacementRevision: uint64(placement.Revision), ExpectedCredentialGeneration: uint64(placement.CredentialGeneration), Bytes: 100,
		Rates: controlplane.Rates{Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}},
	}); !errors.Is(err, ErrFleetUsage) {
		t.Fatalf("unknown group provenance funded first pool: %v", err)
	}
	var grants int64
	if err := f.db.Model(&model.FleetGrant{}).Count(&grants).Error; err != nil || grants != 0 {
		t.Fatalf("unknown group opening leaked quota authority: %d %v", grants, err)
	}
	if err := f.db.Create(&previousPin).Error; err != nil {
		t.Fatal(err)
	}
	groupFleetCredit(t, f, placement, finished.Term, 100)
	var budget model.FleetBudget
	if err := f.db.Table("fleet_budgets b").Select("b.*").Joins("JOIN fleet_budget_scopes s ON s.budget_id=b.id").Where("s.kind='group' AND s.source_id=?", group.Id).Take(&budget).Error; err != nil || budget.UsedBytes != 120 || budget.ReservedBytes != 100 {
		t.Fatalf("first group funding lost reset-era opening or billed reservations: %+v %v", budget, err)
	}
}

func TestFleetGroupFirstFundingRefusesUnpinnedProvisionalSeedPostgres(t *testing.T) {
	f, _ := groupedFleetFixture(t, 0, 0)
	placement := f.approve(t, f.materialize(t, 0))
	active := f.activate(t, placement)
	other := f.client
	other.Id, other.Email, other.UUID = 0, "unproven-group-seed", uuid.NewString()
	if err := f.db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	for _, inbound := range f.inbounds {
		if err := f.db.Create(&model.ClientInbound{ClientId: other.Id, InboundId: inbound.Id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.Create(&core.ClientTraffic{Email: other.Email, InboundId: f.inbounds[0].Id, Up: 25, Total: other.TotalGB, Enable: true, ExpiryTime: other.ExpiryTime}).Error; err != nil {
		t.Fatal(err)
	}
	provisional := f
	provisional.client = other
	provisional.approve(t, provisional.materialize(t, 1))
	if _, err := (FleetClientService{}).AllocateCredit(t.Context(), FleetCreditRequest{
		OperationID: uuid.NewString(), PlacementID: placement.ID, TermID: active.Term.ID,
		ExpectedPlacementRevision: uint64(placement.Revision), ExpectedCredentialGeneration: uint64(placement.CredentialGeneration), Bytes: 100,
		Rates: controlplane.Rates{Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}},
	}); !errors.Is(err, ErrFleetUsage) {
		t.Fatalf("unproven provisional group seed funded authority: %v", err)
	}
	for _, row := range []any{&model.FleetGrant{}, &model.FleetBudgetScope{}} {
		var count int64
		if err := f.db.Model(row).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("unproven group seed left authority or a pool: %T %d %v", row, count, err)
		}
	}
}

func TestFleetGroupFiniteQuotaCannotAdoptPreviouslyUnparentedGrantPostgres(t *testing.T) {
	f, group := groupedFleetFixture(t, 0, 0)
	if err := f.db.Model(&model.GroupQuota{}).Where("name = ?", group.Name).Update("quota_bytes", 0).Error; err != nil {
		t.Fatal(err)
	}
	placement := f.approve(t, f.materialize(t, 0))
	active := f.activate(t, placement)
	credit := groupFleetCredit(t, f, placement, active.Term, 200)
	fleetUsageReport(t, f, 0, credit.Grant, 1, 30, 20)
	fleetAppliedStop(t, f, f.nodes[0], active.Term, credit)
	if err := f.db.Model(&model.GroupQuota{}).Where("name = ?", group.Name).Update("quota_bytes", 700).Error; err != nil {
		t.Fatal(err)
	}
	placement = f.approve(t, f.materialize(t, 0))
	pending, err := (FleetClientService{}).Transition(t.Context(), &f.admin, FleetTransitionRequest{
		OperationID: uuid.NewString(), PlacementID: placement.ID, Kind: controlplane.TrafficReset,
		ExpectedPlacementRevision: uint64(placement.Revision), ExpectedCredentialGeneration: uint64(placement.CredentialGeneration),
	})
	if err != nil {
		t.Fatal(err)
	}
	finished, err := (FleetClientService{}).FinishTransition(t.Context(), &f.admin, FleetFinishRequest{
		OperationID: uuid.NewString(), PlacementID: placement.ID, PendingTermID: pending.Term.ID,
		ExpectedPlacementRevision: uint64(placement.Revision), ExpectedCredentialGeneration: uint64(placement.CredentialGeneration),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (FleetClientService{}).AllocateCredit(t.Context(), FleetCreditRequest{
		OperationID: uuid.NewString(), PlacementID: placement.ID, TermID: finished.Term.ID,
		ExpectedPlacementRevision: uint64(placement.Revision), ExpectedCredentialGeneration: uint64(placement.CredentialGeneration), Bytes: 100,
		Rates: controlplane.Rates{Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}},
	}); !errors.Is(err, ErrFleetPool) {
		t.Fatalf("unparented historical grant was adopted into a new finite pool: %v", err)
	}
	var scopes int64
	if err := f.db.Model(&model.FleetBudgetScope{}).Where("kind='group' AND source_id=?", group.Id).Count(&scopes).Error; err != nil || scopes != 0 {
		t.Fatalf("blocked quota conversion persisted a new parent pool: %d %v", scopes, err)
	}
	fleetUsageReport(t, f, 0, credit.Grant, 2, 40, 20)
	var old model.FleetBudget
	if err := f.db.First(&old, "id = ?", active.Term.BudgetID).Error; err != nil || old.UsedBytes != 60 || old.ReservedBytes != 140 {
		t.Fatalf("blocked conversion changed late old authority accounting: %+v %v", old, err)
	}
}
