package service

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func TestFleetApprovalRequiresMasterAuthorityAndLegacyQuiescence(t *testing.T) {
	f := fleetFixture(t, false)
	actor := model.User{Username: "reseller-fleet", Role: "reseller"}
	if err := f.db.Create(&actor).Error; err != nil {
		t.Fatal(err)
	}
	request := FleetMaterializeRequest{
		PanelClientID: f.client.Id, NodeID: f.nodes[0].NodeID, InstallationID: f.nodes[0].InstallationID,
		RuntimeID: "xray", CoreID: "xray", InboundIDs: []int{f.inbounds[0].Id, f.inbounds[1].Id},
	}
	if _, err := (FleetClientService{}).Materialize(t.Context(), &actor, request); !errors.Is(err, ErrResellerForbidden) {
		t.Fatalf("non-master created placement=%v", err)
	}
	request.InboundIDs = append(request.InboundIDs, 999999)
	if _, err := (FleetClientService{}).Materialize(t.Context(), &f.admin, request); !errors.Is(err, ErrFleetSource) {
		t.Fatalf("unassigned inbound expanded placement=%v", err)
	}
	if err := f.db.Model(&f.inbounds[0]).Update("enable", true).Error; err != nil {
		t.Fatal(err)
	}
	materialized := f.materialize(t, 0)
	approval := FleetApproveRequest{
		PlacementID: materialized.Placement.ID, ExpectedRevision: uint64(materialized.Placement.Revision),
		DefinitionDigest: materialized.Placement.DefinitionDigest, SourcePolicyDigest: materialized.Placement.SourcePolicyDigest, GraphDigest: make([]byte, 32),
	}
	if _, err := (FleetClientService{}).Approve(t.Context(), &f.admin, approval); !errors.Is(err, ErrFleetSource) {
		t.Fatalf("live legacy service and fleet acquired same client=%v", err)
	}
	var client model.FleetClient
	if err := f.db.First(&client, "id = ?", materialized.Client.ID).Error; err != nil || client.Owned {
		t.Fatalf("failed approval changed ownership=%+v err=%v", client, err)
	}
	if err := f.db.Model(&f.inbounds[0]).Update("enable", false).Error; err != nil {
		t.Fatal(err)
	}
	updated := f.materialize(t, 0)
	approval.ExpectedRevision, approval.DefinitionDigest = uint64(updated.Placement.Revision), updated.Placement.DefinitionDigest
	if err := f.store.HoldForRecovery(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := (FleetClientService{}).Approve(t.Context(), &f.admin, approval); !errors.Is(err, controlplane.ErrRecovery) {
		t.Fatalf("recovery hold allowed ownership transfer=%v", err)
	}
}

func TestFleetPlacementRejectsNormalizedTransportDrift(t *testing.T) {
	f := fleetFixture(t, false)
	placement := f.approve(t, f.materialize(t, 0))
	active := f.activate(t, placement)
	rollback := errors.New("restore transport fixture")
	for _, tc := range []struct {
		name  string
		model any
		where string
		value any
	}{
		{"flow_override", &model.ClientInbound{}, "client_id = ?", "xtls-rprx-vision"},
		{"security", &model.ClientRecord{}, "id = ?", "chacha20-poly1305"},
		{"reverse", &model.ClientRecord{}, "id = ?", `{"tag":"changed"}`},
		{"wg_allowed_ips", &model.ClientRecord{}, "id = ?", "10.90.1.7/32"},
		{"wg_keep_alive", &model.ClientRecord{}, "id = ?", int64(25)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := f.db.Transaction(func(tx *gorm.DB) error {
				if err := tx.Model(tc.model).Where(tc.where, f.client.Id).Update(tc.name, tc.value).Error; err != nil {
					return err
				}
				_, err := AllocateCreditTx(t.Context(), tx, FleetCreditRequest{
					OperationID: uuid.NewString(), PlacementID: placement.ID, TermID: active.Term.ID,
					ExpectedPlacementRevision: 1, ExpectedCredentialGeneration: 1, Bytes: 100,
					Rates: controlplane.Rates{Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}},
				})
				if !errors.Is(err, ErrFleetSource) {
					t.Errorf("changed normalized transport received authority=%v", err)
				}
				return rollback
			}); !errors.Is(err, rollback) {
				t.Fatal(err)
			}
		})
	}
}
