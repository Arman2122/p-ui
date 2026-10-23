package service

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func TestFleetSourceNativeCreditRefillRequiresExactAppliedReceiptPostgres(t *testing.T) {
	f, network, initial := fleetNativePublicationFixture(t, model.OCServ)
	for _, enabled := range []bool{false, true} {
		if _, err := SetFleetClientEnabledTx(t.Context(), f.db, &f.admin, sourceEnableRequest(t, f, initial.NativeBindings[0].ClientID, enabled)); err != nil {
			t.Fatal(err)
		}
	}
	var work model.FleetSourceWork
	if err := f.db.First(&work, "placement_id = ?", initial.NativeBindings[0].PlacementID).Error; err != nil {
		t.Fatal(err)
	}
	work, err := ProcessFleetSourceWork(t.Context(), f.db, work, StopFleetSourceWorkTx)
	if err != nil || work.RequiresStop || !work.Pending {
		t.Fatalf("empty native stop did not settle=%+v err=%v", work, err)
	}
	preview, err := network.Preview(t.Context(), &f.admin, FleetNetworkPreviewRequest{NodeID: initial.NodeID, InstallationID: initial.InstallationID, ExpectedRevision: initial.NodeRevision.Revision, ExpectedDefaultsID: initial.Defaults.ID})
	if err != nil {
		t.Fatal(err)
	}
	rates := controlplane.Rates{Upload: limits.Rate{Mode: limits.Limited, BitsPerSecond: 1_000_000, BurstBytes: 65536}, Download: limits.Rate{Mode: limits.Limited, BitsPerSecond: 2_000_000, BurstBytes: 65536}}
	if _, err := network.PublishFunded(t.Context(), &f.admin, FleetNetworkPublishRequest{OperationID: uuid.NewString(), CompilationID: preview.ID}, []FleetNetworkFunding{{PlacementID: work.PlacementID, Bytes: 100, Rates: rates}}); err != nil {
		t.Fatal(err)
	}
	base := applyFleetSourcePublication(t, f)
	var grant model.FleetGrant
	var placement model.FleetPlacement
	if err := f.db.First(&grant, "placement_id = ?", work.PlacementID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.First(&placement, "id = ?", work.PlacementID).Error; err != nil {
		t.Fatal(err)
	}
	reportFleetSourceNativeCredit(t, f, grant, base, 1)
	credit, err := RefillCreditTx(t.Context(), f.db, FleetRefillRequest{
		PlacementID: placement.ID, GrantID: grant.ID, TermID: grant.TermID, ExpectedPlacementRevision: uint64(placement.Revision),
		ExpectedCredentialGeneration: uint64(placement.CredentialGeneration), AuthoritySequence: 1, LowWaterBytes: 40, TargetBytes: 100,
	})
	if err != nil || credit.Grant.IssuedBytes != 180 {
		t.Fatalf("native refill=%+v err=%v", credit, err)
	}
	var runtime model.FleetRuntimeJournal
	if err := f.db.First(&runtime, "node_id = ? AND runtime_id = ?", work.NodeID, work.RuntimeID).Error; err != nil {
		t.Fatal(err)
	}
	batch, err := f.store.FetchCredit(t.Context(), f.nodes[0], &nodev1.CreditDeliveryRequest{
		NodeId: work.NodeID, InstallationId: work.InstallationID, RuntimeId: work.RuntimeID,
		AgentStateJournalId: f.hello(t, f.nodes[0]).AgentStateJournalId, StateJournalId: runtime.StateJournalID, CreditJournalId: runtime.CreditJournalID,
	}, ValidateFleetCreditDeliveryTx)
	if err != nil || len(batch.Updates) != 1 || batch.Updates[0].CreditCeiling != 180 {
		t.Fatalf("native refill delivery=%+v err=%v", batch, err)
	}
	reportFleetSourceNativeCredit(t, f, credit.Grant, base, 2)
	noAction := func(context.Context, *gorm.DB, model.FleetSourceWork) error { return nil }
	for _, wrongBase := range []bool{false, true} {
		bad := proto.Clone(batch).(*nodev1.CreditUpdateBatch)
		if wrongBase {
			bad.BaseConfigurationRevision++
		} else {
			bad.Updates[0].CreditRevision, bad.Updates[0].CreditCeiling = uint64(grant.Revision), uint64(grant.IssuedBytes)
		}
		if _, err := f.store.AcknowledgeCredit(t.Context(), f.nodes[0], bad); !errors.Is(err, controlplane.ErrRevision) {
			t.Fatalf("invalid native credit receipt accepted=%v wrongBase=%v", err, wrongBase)
		}
	}
	pending, err := ProcessFleetSourceWork(t.Context(), f.db, work, noAction)
	if err != nil || !pending.Pending {
		t.Fatalf("native report replaced explicit credit ACK=%+v err=%v", pending, err)
	}
	if _, err := f.store.AcknowledgeCredit(t.Context(), f.nodes[0], batch); err != nil {
		t.Fatal(err)
	}
	completed, err := ProcessFleetSourceWork(t.Context(), f.db, pending, noAction)
	if err != nil || completed.Pending {
		t.Fatalf("acknowledged native refill stranded source work=%+v err=%v", completed, err)
	}
}
