package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func sourceApprovalDraft(t *testing.T, f authorityFixture, network *FleetNetworkService, node FleetNodeNetworkView) {
	t.Helper()
	base := FleetRuntimeBase{Core: "xray", Configuration: json.RawMessage(`{"outbounds":[{"protocol":"freedom","tag":"direct"},{"protocol":"blackhole","tag":"manual-draft"}]}`)}
	if _, err := network.ConfigureNode(t.Context(), &f.f.admin, FleetNodeNetworkRequest{
		NodeID: node.NodeID, InstallationID: node.InstallationID, ExpectedRevision: node.Revision.Revision,
		Operations: []FleetNetworkOperation{{Section: "base", Key: "xray", Operation: "replace", Base: &base}},
	}); err != nil {
		t.Fatal(err)
	}
}

func TestFleetSourceInitialNetworkRequiresExplicitApprovalPostgres(t *testing.T) {
	f, work := sourceEnableFixtureWithNetwork(t, false, nil)
	held, err := ProcessFleetSourceWork(t.Context(), f.f.db, work, ReconcileFleetSourceWorkTx)
	if err != nil || !held.Pending || held.Diagnostic != fleetSourceNetworkApprovalRequired || held.RequiresStop {
		t.Fatalf("unpublished network was implicitly approved: work=%+v err=%v", held, err)
	}
	var node model.FleetNode
	if err := f.f.db.First(&node, "id = ?", work.NodeID).Error; err != nil || node.DesiredRevision != int64(f.request.BaseConfigurationRevision) {
		t.Fatalf("initial approval hold published node=%+v err=%v", node, err)
	}
	var compilations int64
	if err := f.f.db.Model(&model.FleetNetworkCompilation{}).Count(&compilations).Error; err != nil || compilations != 0 {
		t.Fatalf("initial hold compiled unpublished network=%d err=%v", compilations, err)
	}
}

func TestFleetSourceDraftHoldDoesNotDelayStopAndCannotSurviveSupersessionPostgres(t *testing.T) {
	f, work := sourceEnableFixtureWithNetwork(t, true, func(f authorityFixture, network *FleetNetworkService, node FleetNodeNetworkView) {
		sourceApprovalDraft(t, f, network, node)
	})
	if work.RequiresStop || work.AppliedStopRevision != int64(f.request.BaseConfigurationRevision) {
		t.Fatalf("manual draft held required stop=%+v", work)
	}
	if err := f.f.db.Model(&work).Update("available_unix_milli", 0).Error; err != nil {
		t.Fatal(err)
	}
	minimumRetry := time.Now().Add(time.Second).UnixMilli()
	held, err := ProcessFleetSourceWork(t.Context(), f.f.db, work, ReconcileFleetSourceWorkTx)
	if err != nil || !held.Pending || held.Diagnostic != fleetSourceNetworkApprovalRequired || held.AvailableUnixMilli < minimumRetry {
		t.Fatalf("draft lacked bounded pending hold=%+v err=%v", held, err)
	}
	var before model.FleetNode
	if err := f.f.db.First(&before, "id = ?", work.NodeID).Error; err != nil || before.DesiredRevision != before.AppliedRevision || before.DesiredRevision != int64(f.request.BaseConfigurationRevision) {
		t.Fatalf("manual draft escaped source hold=%+v err=%v", before, err)
	}
	if _, err := SetFleetClientEnabledTx(t.Context(), f.f.db, &f.f.admin, sourceEnableRequest(t, f.f, work.ClientID, false)); err != nil {
		t.Fatal(err)
	}
	var stopped model.FleetSourceWork
	if err := f.f.db.First(&stopped, "placement_id = ?", work.PlacementID).Error; err != nil || !stopped.RequiresStop || stopped.Diagnostic != "" || stopped.Revision <= held.Revision {
		t.Fatalf("fresh stop inherited publication hold=%+v err=%v", stopped, err)
	}
	if _, err := ProcessFleetSourceWork(t.Context(), f.f.db, held, ReconcileFleetSourceWorkTx); !errors.Is(err, controlplane.ErrRevision) {
		t.Fatalf("superseded materialization was accepted=%v", err)
	}
	stopped, err = ProcessFleetSourceWork(t.Context(), f.f.db, stopped, StopFleetSourceWorkTx)
	if err != nil || stopped.Pending || stopped.RequiresStop || stopped.Diagnostic != "" {
		t.Fatalf("new stop did not advance old actual proof=%+v err=%v", stopped, err)
	}
	var budget model.FleetBudget
	if err := f.f.db.First(&budget, "id = ?", f.active.Term.BudgetID).Error; err != nil || budget.ReservedBytes != 300 || budget.UsedBytes != 0 {
		t.Fatalf("approval hold changed allowance=%+v err=%v", budget, err)
	}
}

func TestFleetSourceDisabledEnableCanMaterializeThenExplicitlyPublishWithoutFundingPostgres(t *testing.T) {
	f := fleetFixture(t, true)
	network, err := NewFleetNetworkService(f.db)
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := network.ApproveDefaults(t.Context(), &f.admin, 0, FleetNetworkSpec{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	node, err := network.ConfigureNode(t.Context(), &f.admin, FleetNodeNetworkRequest{NodeID: f.nodes[0].NodeID, InstallationID: f.nodes[0].InstallationID})
	if err != nil {
		t.Fatal(err)
	}
	request := FleetNetworkPreviewRequest{NodeID: node.NodeID, InstallationID: node.InstallationID, ExpectedRevision: node.Revision.Revision, ExpectedDefaultsID: defaults.ID}
	preview, err := network.Preview(t.Context(), &f.admin, request)
	if err != nil || !preview.Ready {
		t.Fatalf("empty approved baseline=%+v err=%v", preview.Diagnostics, err)
	}
	if _, err := network.Publish(t.Context(), &f.admin, FleetNetworkPublishRequest{OperationID: uuid.NewString(), CompilationID: preview.ID}); err != nil {
		t.Fatal(err)
	}
	applyFleetSourcePublication(t, f)
	if err := f.db.Model(&model.ClientRecord{}).Where("id = ?", f.client.Id).Update("enable", false).Error; err != nil {
		t.Fatal(err)
	}
	materialized := f.materialize(t, 0)
	materialized.Placement = f.approve(t, materialized)
	disabledPolicy := bytes.Clone(materialized.Placement.SourcePolicyDigest)
	sourceApprovalDraft(t, authorityFixture{f: f}, network, node)
	if _, err := SetFleetClientEnabledTx(t.Context(), f.db, &f.admin, sourceEnableRequest(t, f, materialized.Client.ID, true)); err != nil {
		t.Fatal(err)
	}
	var work model.FleetSourceWork
	if err := f.db.First(&work, "placement_id = ?", materialized.Placement.ID).Error; err != nil {
		t.Fatal(err)
	}
	work, err = ProcessFleetSourceWork(t.Context(), f.db, work, StopFleetSourceWorkTx)
	if err != nil || work.RequiresStop || !work.Pending {
		t.Fatalf("initial no-authority stop=%+v err=%v", work, err)
	}
	work, err = ProcessFleetSourceWork(t.Context(), f.db, work, ReconcileFleetSourceWorkTx)
	if err != nil || !work.Pending || work.Diagnostic != fleetSourceNetworkApprovalRequired {
		t.Fatalf("disabled enable published draft=%+v err=%v", work, err)
	}
	var prepared model.FleetPlacement
	if err := f.db.First(&prepared, "id = ?", materialized.Placement.ID).Error; err != nil || bytes.Equal(prepared.SourcePolicyDigest, disabledPolicy) || prepared.Enabled || len(prepared.GraphDigest) != 0 {
		t.Fatalf("held materialization was lost or approved=%+v err=%v", prepared, err)
	}
	for _, table := range []any{&model.FleetTerm{}, &model.FleetGrant{}} {
		var count int64
		if err := f.db.Model(table).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("held source materialization created authority %T=%d err=%v", table, count, err)
		}
	}
	request.ExpectedRevision++
	preview, err = network.Preview(t.Context(), &f.admin, request)
	if err != nil || !preview.Ready {
		t.Fatalf("held source prevented explicit admin preview=%+v err=%v", preview.Diagnostics, err)
	}
	if _, err := network.Publish(t.Context(), &f.admin, FleetNetworkPublishRequest{OperationID: uuid.NewString(), CompilationID: preview.ID}); err != nil {
		t.Fatal(err)
	}
	work, err = ProcessFleetSourceWork(t.Context(), f.db, work, ReconcileFleetSourceWorkTx)
	if err != nil || !work.Pending {
		t.Fatalf("publication receipt completed approval before APPLIED=%+v err=%v", work, err)
	}
	base := applyFleetSourcePublication(t, f)
	work, err = ProcessFleetSourceWork(t.Context(), f.db, work, ReconcileFleetSourceWorkTx)
	if err != nil || work.Pending || work.Diagnostic != "" {
		t.Fatalf("actual APPLIED did not clear hold=%+v err=%v", work, err)
	}
	var grants, terms int64
	if err := f.db.Model(&model.FleetGrant{}).Count(&grants).Error; err != nil || grants != 0 {
		t.Fatalf("admin publication funded without demand=%d err=%v", grants, err)
	}
	if err := f.db.Model(&model.FleetTerm{}).Count(&terms).Error; err != nil || terms != 0 {
		t.Fatalf("admin publication started billing=%d err=%v", terms, err)
	}
	if err := f.db.First(&materialized.Placement, "id = ?", prepared.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.First(&materialized.Client, "id = ?", work.ClientID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Where("placement_id = ?", work.PlacementID).Find(&materialized.Inbounds).Error; err != nil {
		t.Fatal(err)
	}
	generation, err := f.store.BeginSession(t.Context(), f.nodes[0], f.hello(t, f.nodes[0]))
	if err != nil {
		t.Fatal(err)
	}
	fleetAuthorityReport(t, f, 1, time.Now())
	demand := fleetDemand(materialized)
	demand.DeploymentRevision = base
	if _, err := HandleFleetActivation(t.Context(), f.db, f.nodes[0], generation, demand); err != nil {
		t.Fatal(err)
	}
	var pending model.FleetAdmissionDemand
	if err := f.db.First(&pending, "placement_id = ?", work.PlacementID).Error; err != nil {
		t.Fatal(err)
	}
	leases, err := f.store.ClaimAdmissionNodes(t.Context(), 1)
	if err != nil || len(leases) != 1 {
		t.Fatalf("fresh demand lease=%+v err=%v", leases, err)
	}
	if result, err := ProcessFleetAdmissionDemand(t.Context(), f.db, leases[0], pending); err != nil || result.Outcome != FleetAdmissionSatisfied {
		t.Fatalf("fresh demand did not fund approved source=%+v err=%v", result, err)
	}
	if err := f.db.Model(&model.FleetGrant{}).Count(&grants).Error; err != nil || grants != 1 {
		t.Fatalf("fresh demand grant count=%d err=%v", grants, err)
	}
}
