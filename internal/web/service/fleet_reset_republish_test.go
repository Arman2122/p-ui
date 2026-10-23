package service

import (
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestFleetScheduledResetRecompilesClosedTermAsAuthOnlyAndRequiresFreshDemandPostgres(t *testing.T) {
	f, _ := sourceEnableFixture(t)
	schedule := dueFleetResetFixture(t, f)
	if _, err := advanceFleetResetSchedule(t.Context(), f.f.db, schedule); err != nil {
		t.Fatal(err)
	}
	schedule = readFleetResetSchedule(t, f.f.db, schedule.ClientID)
	if _, err := advanceFleetResetSchedule(t.Context(), f.f.db, schedule); err != nil {
		t.Fatal(err)
	}
	completedSchedule := readFleetResetSchedule(t, f.f.db, schedule.ClientID)
	var work model.FleetSourceWork
	if err := f.f.db.First(&work, "placement_id = ?", f.materialized.Placement.ID).Error; err != nil {
		t.Fatal(err)
	}
	if !work.RequiresStop {
		t.Fatal("completion erased an unadvanced source stop obligation")
	}
	work, err := ProcessFleetSourceWork(t.Context(), f.f.db, work, StopFleetSourceWorkTx)
	if err != nil || work.RequiresStop || !work.Pending {
		t.Fatalf("closed term source stop did not settle=%+v err=%v", work, err)
	}
	pending, err := ProcessFleetSourceWork(t.Context(), f.f.db, work, ReconcileFleetSourceWorkTx)
	if err != nil || !pending.Pending || pending.RequiresStop {
		t.Fatalf("closed term prevented replacement authentication graph=%+v err=%v", pending, err)
	}
	base := applyFleetSourcePublication(t, f.f)
	if converged, err := ProcessFleetSourceWork(t.Context(), f.f.db, pending, ReconcileFleetSourceWorkTx); err != nil || converged.Pending {
		t.Fatalf("post-reset graph did not converge after APPLIED=%+v err=%v", converged, err)
	}
	scope, err := controlplane.PublishedAuthorityScopeTx(f.f.db, work.NodeID, base, work.RuntimeID, work.ClientID)
	if err != nil || scope.BaseGrant != nil || base != f.request.BaseConfigurationRevision+1 {
		t.Fatalf("post-reset graph forwarded before fresh demand=%+v err=%v base=%d", scope, err, base)
	}
	var materialized FleetMaterialization
	if err := f.f.db.First(&materialized.Placement, "id = ?", work.PlacementID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.f.db.First(&materialized.Client, "id = ?", work.ClientID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.f.db.Where("placement_id = ?", work.PlacementID).Find(&materialized.Inbounds).Error; err != nil {
		t.Fatal(err)
	}
	if materialized.Placement.CurrentGrantID != nil || materialized.Client.CurrentTermID == nil || !sameFleetResetID(materialized.Client.CurrentTermID, completedSchedule.BoundTermID) {
		t.Fatalf("post-reset placement retained old grant or wrong term=%+v", materialized)
	}
	generation, err := f.f.store.BeginSession(t.Context(), f.f.nodes[0], f.f.hello(t, f.f.nodes[0]))
	if err != nil {
		t.Fatal(err)
	}
	fleetAuthorityReport(t, f.f, 2, time.Now())
	request := fleetDemand(materialized)
	request.DeploymentRevision = base
	if result, err := HandleFleetActivation(t.Context(), f.f.db, f.f.nodes[0], generation, request); err != nil || result.GetTermId() != *completedSchedule.BoundTermID {
		t.Fatalf("fresh demand did not bind replacement term=%+v err=%v", result, err)
	}
	var demand model.FleetAdmissionDemand
	if err := f.f.db.First(&demand, "placement_id = ?", work.PlacementID).Error; err != nil {
		t.Fatal(err)
	}
	leases, err := f.f.store.ClaimAdmissionNodes(t.Context(), 1)
	if err != nil || len(leases) != 1 {
		t.Fatalf("post-reset demand lease=%+v err=%v", leases, err)
	}
	result, err := ProcessFleetAdmissionDemand(t.Context(), f.f.db, leases[0], demand)
	if err != nil || result.Outcome != FleetAdmissionSatisfied {
		t.Fatalf("post-reset fresh demand allocation=%+v err=%v", result, err)
	}
	f.request.BaseConfigurationRevision, f.request.BasePayloadSha256 = base, scope.PayloadSHA256
	batch, err := f.f.store.FetchAuthorityUpdates(t.Context(), f.f.nodes[0], f.request, ValidateFleetAuthorityDeliveryTx)
	if err != nil || batch == nil || len(batch.Updates) != 1 || !batch.Updates[0].Enabled || batch.Updates[0].GrantId == f.credit.Grant.ID || batch.Updates[0].TermId != *completedSchedule.BoundTermID {
		t.Fatalf("fresh demand reused retired grant or wrong allowance=%+v err=%v", batch, err)
	}
	var oldBudget model.FleetBudget
	if err := f.f.db.First(&oldBudget, "id = ?", f.active.Term.BudgetID).Error; err != nil || oldBudget.UsedBytes != 0 || oldBudget.ReservedBytes != f.credit.Grant.IssuedBytes {
		t.Fatalf("new demand changed old unresolved accounting=%+v err=%v", oldBudget, err)
	}
}
