package service

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
	webruntime "github.com/Arman2122/p-ui/internal/web/runtime"
)

func TestFleetPlacementPreferencesFenceAuthorityBeforeFanoutPostgres(t *testing.T) {
	for _, action := range []string{"restrict", "detach"} {
		t.Run(action, func(t *testing.T) {
			f := newAuthorityFixture(t)
			f.queue(t, true)
			ids := []int{}
			request := FleetPlacementPreferenceRequest{OperationID: uuid.NewString(), ClientID: f.materialized.Client.ID, ExpectedRevision: 1, Action: action, InboundIDs: &ids}
			if action == "detach" {
				ids, request.NodeID = []int{f.f.inbounds[0].Id}, f.f.nodes[0].NodeID
			}
			result, err := SetFleetClientPlacementTx(t.Context(), f.f.db, &f.f.admin, request)
			if err != nil || result.Revision != 2 || !result.Pending {
				t.Fatalf("preference mutation=%+v err=%v", result, err)
			}
			replay, err := SetFleetClientPlacementTx(t.Context(), f.f.db, &f.f.admin, request)
			if err != nil || !replay.Replayed || replay.Revision != 2 {
				t.Fatalf("preference replay=%+v err=%v", replay, err)
			}
			if _, err := AllocateCreditTx(t.Context(), f.f.db, FleetCreditRequest{
				OperationID: uuid.NewString(), PlacementID: f.materialized.Placement.ID, TermID: f.active.Term.ID,
				ExpectedPlacementRevision: uint64(f.materialized.Placement.Revision), ExpectedCredentialGeneration: uint64(f.materialized.Client.CredentialGeneration), Bytes: 100,
			}); !errors.Is(err, controlplane.ErrDrain) {
				t.Fatalf("committed preference did not fence allocation before fanout: %v", err)
			}
			if _, err := RefillCreditTx(t.Context(), f.f.db, FleetRefillRequest{
				PlacementID: f.materialized.Placement.ID, GrantID: f.credit.Grant.ID, TermID: f.active.Term.ID,
				ExpectedPlacementRevision: uint64(f.materialized.Placement.Revision), ExpectedCredentialGeneration: uint64(f.materialized.Client.CredentialGeneration), AuthoritySequence: 1, LowWaterBytes: 40, TargetBytes: 200,
			}); !errors.Is(err, controlplane.ErrDrain) {
				t.Fatalf("committed preference did not fence refill before fanout: %v", err)
			}
			batch, err := f.f.store.FetchAuthorityUpdates(t.Context(), f.f.nodes[0], f.request, ValidateFleetAuthorityDeliveryTx)
			if err != nil || len(batch.Updates) != 0 {
				t.Fatalf("committed preference allowed enabled authority delivery: %+v %v", batch, err)
			}
			var stop model.FleetSourceWork
			if err := f.f.db.First(&stop, "placement_id = ?", f.materialized.Placement.ID).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := ProcessFleetSourceWork(t.Context(), f.f.db, stop, StopFleetSourceWorkTx); err != nil {
				t.Fatal(err)
			}
			batch, err = f.f.store.FetchAuthorityUpdates(t.Context(), f.f.nodes[0], f.request, ValidateFleetAuthorityDeliveryTx)
			if err != nil || len(batch.Updates) != 1 {
				t.Fatalf("preference blocked frozen disabled authority: %+v %v", batch, err)
			}
			if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], batch); err != nil {
				t.Fatal(err)
			}
			settled, err := ProcessFleetSourceWork(t.Context(), f.f.db, stop, StopFleetSourceWorkTx)
			if err != nil || settled.RequiresStop {
				t.Fatalf("applied stop did not settle: %+v %v", settled, err)
			}
			current := f.f.materialize(t, 0)
			current.Placement = f.f.approve(t, current)
			if _, err := AllocateCreditTx(t.Context(), f.f.db, FleetCreditRequest{
				OperationID: uuid.NewString(), PlacementID: f.materialized.Placement.ID, TermID: f.active.Term.ID,
				ExpectedPlacementRevision: uint64(current.Placement.Revision), ExpectedCredentialGeneration: uint64(current.Client.CredentialGeneration), Bytes: 100,
			}); !errors.Is(err, controlplane.ErrDrain) {
				t.Fatalf("missing intent bypassed current restriction after stop ACK: %v", err)
			}
			var grant model.FleetGrant
			if err := f.f.db.First(&grant, "id = ?", f.credit.Grant.ID).Error; err != nil || grant.IssuedBytes != f.credit.Grant.IssuedBytes || grant.Revision != f.credit.Grant.Revision {
				t.Fatalf("preference mutated existing quota: %+v %v", grant, err)
			}
			var work model.FleetPlacementFanout
			if err := f.f.db.First(&work, "scope = 'client' AND scope_id = ?", f.materialized.Client.ID).Error; err != nil || !work.Pending || work.Revision != 1 {
				t.Fatalf("mutation/replay did not retain one durable fanout: %+v %v", work, err)
			}
		})
	}
}

func TestFleetPlacementSuppressionIsScopedAndReattachIsExplicitPostgres(t *testing.T) {
	f := fleetFixture(t, false)
	first := f.materialize(t, 0)
	first.Placement = f.approve(t, first)
	second := f.materialize(t, 1)
	second.Placement = f.approve(t, second)
	ids := []int{f.inbounds[0].Id}
	request := FleetPlacementPreferenceRequest{OperationID: uuid.NewString(), ClientID: first.Client.ID, ExpectedRevision: 1, Action: "detach", NodeID: f.nodes[0].NodeID, InboundIDs: &ids}
	if _, err := SetFleetClientPlacementTx(t.Context(), f.db, &f.admin, request); err != nil {
		t.Fatal(err)
	}
	err := f.db.Transaction(func(tx *gorm.DB) error {
		if err := requireFleetPlacementSourceReadyTx(tx, first.Placement); !errors.Is(err, controlplane.ErrDrain) {
			t.Fatalf("scoped detach did not fence its node: %v", err)
		}
		if err := requireFleetPlacementSourceReadyTx(tx, second.Placement); err != nil {
			t.Fatalf("scoped detach blocked another node: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	request.OperationID, request.ExpectedRevision, request.Action = uuid.NewString(), 2, "attach"
	if _, err := SetFleetClientPlacementTx(t.Context(), f.db, &f.admin, request); err != nil {
		t.Fatal(err)
	}
	_, preferences, err := fleetPlacementPreferencesTx(f.db, first.Client.ID)
	if err != nil || preferences.InboundIDs != nil || len(preferences.SuppressedTargets[f.nodes[0].NodeID]) != 0 ||
		!slices.Contains(preferences.ManualTargets[f.nodes[0].NodeID], f.inbounds[0].Id) || !slices.Contains(preferences.ManualTargets[f.nodes[1].NodeID], f.inbounds[0].Id) {
		t.Fatalf("reattach changed unrelated/manual target restrictions: %+v %v", preferences, err)
	}
	var work model.FleetSourceWork
	if err := f.db.First(&work, "placement_id = ?", first.Placement.ID).Error; err != nil || !work.RequiresStop || !work.Pending {
		t.Fatalf("reattach erased the outstanding stop requirement: %+v %v", work, err)
	}
}

func TestFleetPlacementPreferenceRuntimeDoesNotLockNodeAndRollsBackPostgres(t *testing.T) {
	f := fleetFixture(t, false)
	materialized := f.materialize(t, 0)
	f.approve(t, materialized)
	runtime, err := NewFleetRuntime(f.db)
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := runtime.(webruntime.SourcePlacementRuntime)
	ids := []int{f.inbounds[0].Id}
	request := FleetPlacementPreferenceRequest{OperationID: uuid.NewString(), ClientID: materialized.Client.ID, ExpectedRevision: 1, Action: "detach", NodeID: f.nodes[0].NodeID, InboundIDs: &ids}
	locked := f.db.Begin()
	if locked.Error != nil {
		t.Fatal(locked.Error)
	}
	defer locked.Rollback()
	if err := locked.Exec("SELECT id FROM fleet_nodes WHERE id = ? FOR UPDATE", f.nodes[0].NodeID).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	rollback := errors.New("deliberate outer transaction rollback")
	err = f.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if _, err := dispatcher.SetClientPlacementTx(ctx, tx, &f.admin, request); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatalf("preference dispatcher acquired target node lock: %v", err)
	}
	var client model.FleetClient
	if err := f.db.First(&client, "id = ?", materialized.Client.ID).Error; err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := f.db.Model(&model.FleetPlacementFanout{}).Where("scope = 'client'").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if client.PlacementRevision != 1 || client.PlacementPreferences != "{}" || count != 0 {
		t.Fatalf("outer rollback leaked preference or fanout: client=%+v work=%d", client, count)
	}
	if _, err := dispatcher.SetClientPlacementTx(t.Context(), f.db, &f.admin, request); !errors.Is(err, ErrFleetRuntimeDatabase) {
		t.Fatalf("nontransactional placement dispatch accepted: %v", err)
	}
}
