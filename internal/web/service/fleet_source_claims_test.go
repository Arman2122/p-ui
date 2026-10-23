package service

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestFleetSourceCreateRefusesUnqualifiedInheritedAndRetainedOwnersPostgres(t *testing.T) {
	for _, scope := range []string{"plan", "group", "retained_orphan"} {
		t.Run(scope, func(t *testing.T) {
			db, actor, inbound := fleetCreateFixture(t)
			old := model.ClientRecord{Email: "unqualified-owner", UUID: uuid.NewString(), Enable: true}
			if scope == "group" {
				old.Group = "inherited-owner"
			}
			if err := db.Create(&old).Error; err != nil {
				t.Fatal(err)
			}
			switch scope {
			case "plan":
				plan := model.Policy{Name: "unqualified-plan", Rev: 1, Tiers: `[]`}
				if err := db.Create(&plan).Error; err != nil {
					t.Fatal(err)
				}
				for _, row := range []any{&model.ClientPolicy{Email: old.Email, PolicyId: &plan.Id, AppliedRev: 1}, &model.PolicyInbound{PolicyId: plan.Id, InboundId: inbound.Id}} {
					if err := db.Create(row).Error; err != nil {
						t.Fatal(err)
					}
				}
			case "group":
				for _, row := range []any{&model.ClientGroup{Name: old.Group}, &model.GroupInbound{Name: old.Group, InboundID: inbound.Id}} {
					if err := db.Create(row).Error; err != nil {
						t.Fatal(err)
					}
				}
			case "retained_orphan":
				owner := model.FleetClient{ID: uuid.NewString(), PanelClientID: &old.Id, Revision: 1, CredentialGeneration: 1, CredentialDigest: make([]byte, 32), Owned: true}
				node := model.FleetNode{ID: uuid.NewString(), InstallationID: uuid.NewString(), Name: "retained-owner", Enabled: true}
				placement := model.FleetPlacement{ID: uuid.NewString(), ClientID: owner.ID, NodeID: node.ID, InstallationID: node.InstallationID, RuntimeID: "xray", CoreID: "xray", Revision: 1, CredentialGeneration: 1, SourcePolicyDigest: make([]byte, 32), DefinitionDigest: make([]byte, 32)}
				for _, row := range []any{&owner, &node, &placement, &model.FleetPlacementInbound{PlacementID: placement.ID, InstanceID: uuid.NewString(), PanelInbound: &inbound.Id}} {
					if err := db.Create(row).Error; err != nil {
						t.Fatal(err)
					}
				}
				if err := db.Delete(&old).Error; err != nil {
					t.Fatal(err)
				}
			}
			request := fleetCreateRequest(inbound.Id)
			request.Client.ID = old.UUID
			if _, err := CreateFleetClientTx(t.Context(), db, &actor, request); !errors.Is(err, ErrFleetCredentialClaim) {
				t.Fatalf("unqualified %s owner was invisible to credential reservation: %v", scope, err)
			}
			var count int64
			if err := db.Model(&model.ClientRecord{}).Where("email = ?", request.Client.Email).Count(&count).Error; err != nil || count != 0 {
				t.Fatalf("conflicting creation left source=%d err=%v", count, err)
			}
			requireCreateCount(t, db, &model.FleetOperation{}, 0)
			requireCreateCount(t, db, &model.FleetSourceCredentialClaim{}, 0)
		})
	}
}
