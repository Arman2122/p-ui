package service

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestFleetPlacementIntentSupersessionRetainsRealStopAndAuthorityPostgres(t *testing.T) {
	f := newAuthorityFixture(t)
	if _, err := f.f.store.AcknowledgeAuthorityUpdates(t.Context(), f.f.nodes[0], f.queue(t, true)); err != nil {
		t.Fatal(err)
	}
	s, err := NewFleetPlacementService(f.f.db)
	if err != nil {
		t.Fatal(err)
	}
	configurePlacementNode(t, f.f, s, 0, "manual")
	reconcile := func() model.FleetPlacementIntent {
		t.Helper()
		if err := f.f.db.Transaction(func(tx *gorm.DB) error {
			settled, err := reconcileFleetPlacementClientTx(tx, f.materialized.Client.ID)
			if err == nil && !settled {
				t.Fatal("known runtime projection unexpectedly unresolved")
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
		var row model.FleetPlacementIntent
		if err := f.f.db.First(&row, "client_id = ?", f.materialized.Client.ID).Error; err != nil {
			t.Fatal(err)
		}
		return row
	}
	initial := reconcile()
	if !initial.Assigned || initial.MaterializedPlacementID == nil || *initial.MaterializedPlacementID != f.materialized.Placement.ID || initial.RequiresStop {
		t.Fatalf("existing explicit placement lost its origin: %+v", initial)
	}
	if repeated := reconcile(); repeated.Revision != initial.Revision || !repeated.Assigned {
		t.Fatalf("unchanged projection churned assignment: %+v", repeated)
	}
	ids := []int{f.f.inbounds[0].Id, f.f.inbounds[1].Id}
	request := FleetPlacementPreferenceRequest{OperationID: uuid.NewString(), ClientID: f.materialized.Client.ID, ExpectedRevision: 1, Action: "detach", NodeID: f.f.nodes[0].NodeID, InboundIDs: &ids}
	if _, err := SetFleetClientPlacementTx(t.Context(), f.f.db, &f.f.admin, request); err != nil {
		t.Fatal(err)
	}
	retiring := reconcile()
	if retiring.Assigned || !retiring.Pending || !retiring.RequiresStop || retiring.State != "retiring" {
		t.Fatalf("target detach did not retain retiring intent: %+v", retiring)
	}
	request.OperationID, request.ExpectedRevision, request.Action = uuid.NewString(), 2, "attach"
	if _, err := SetFleetClientPlacementTx(t.Context(), f.f.db, &f.f.admin, request); err != nil {
		t.Fatal(err)
	}
	reassigned := reconcile()
	if !reassigned.Assigned || !reassigned.Pending || !reassigned.RequiresStop || reassigned.Revision <= retiring.Revision {
		t.Fatalf("assignment supersession erased applied-stop requirement: %+v", reassigned)
	}
	if err := f.f.db.Transaction(func(tx *gorm.DB) error { return requireFleetPlacementSourceReadyTx(tx, f.materialized.Placement) }); !errors.Is(err, controlplane.ErrDrain) {
		t.Fatalf("reattachment became usable before stop ACK: %v", err)
	}
	var grant model.FleetGrant
	var count int64
	if err := f.f.db.First(&grant, "id = ?", f.credit.Grant.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.f.db.Model(&model.FleetGrant{}).Where("client_id = ?", f.materialized.Client.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 || grant.IssuedBytes != f.credit.Grant.IssuedBytes || grant.Revision != f.credit.Grant.Revision {
		t.Fatalf("assignment retry multiplied or erased authority: count=%d grant=%+v", count, grant)
	}
}
