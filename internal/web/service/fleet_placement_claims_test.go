package service

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestFleetPlacementUnpreparedInheritedCredentialsRemainPendingPostgres(t *testing.T) {
	for _, missingBinding := range []bool{false, true} {
		name, want := "unreserved-identity", ErrFleetCredentialClaim
		if missingBinding {
			name, want = "unprepared-binding", ErrFleetSourceCredentials
		}
		t.Run(name, func(t *testing.T) {
			f := newPlacementApplicationFixture(t)
			initial := f.f.inbounds[0]
			initial.Id, initial.Port, initial.Tag = 0, 25431, uuid.NewString()
			if err := f.f.db.Create(&initial).Error; err != nil {
				t.Fatal(err)
			}
			group := model.ClientGroup{Name: "unprepared-inherited-target"}
			if err := f.f.db.Create(&group).Error; err != nil {
				t.Fatal(err)
			}
			request := fleetCreateRequest(initial.Id)
			request.Client.Group, request.Client.Enable = group.Name, true
			created, err := CreateFleetClientTx(t.Context(), f.f.db, &f.f.admin, request)
			if err != nil {
				t.Fatal(err)
			}
			inherited := initial
			inherited.Id, inherited.Port, inherited.Tag = 0, 25432, uuid.NewString()
			if missingBinding {
				inherited.Protocol, inherited.Settings = model.Shadowsocks, `{"method":"2022-blake3-aes-128-gcm"}`
			}
			if err := f.f.db.Create(&inherited).Error; err != nil {
				t.Fatal(err)
			}
			if err := f.f.db.Create(&model.GroupInbound{Name: group.Name, InboundID: inherited.Id}).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := f.service.ConfigureNode(t.Context(), &f.f.admin, FleetNodePlacementRequest{
				NodeID: f.intent.NodeID, InstallationID: f.intent.InstallationID, ExpectedRevision: 1, Enabled: true,
				InboundIDs: []int{f.f.inbounds[0].Id, f.f.inbounds[1].Id, inherited.Id},
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := f.service.PutRule(t.Context(), &f.f.admin, FleetPlacementRuleRequest{
				ID: uuid.NewString(), Scope: "group", GroupID: &group.Id, Enabled: true, NodeIDs: []string{f.intent.NodeID}, InboundIDs: []int{inherited.Id},
			}); err != nil {
				t.Fatal(err)
			}
			hello := f.f.hello(t, f.f.nodes[1])
			hello.Capabilities = []*nodev1.CoreCapability{{CoreId: "xray", Version: "sha256:qualified-worker", Protocols: []string{"vless", "shadowsocks"}, ClientRateEnforcement: true, ClientQuotaEnforcement: true, ActivationAdmission: true}}
			if _, err := f.f.store.BeginSession(t.Context(), f.f.nodes[1], hello); err != nil {
				t.Fatal(err)
			}
			for range 12 {
				worked, err := ProcessFleetPlacementFanout(t.Context(), f.f.db, 32)
				if err != nil {
					t.Fatal(err)
				}
				if !worked {
					break
				}
			}
			var pending model.FleetPlacementIntent
			if err := f.f.db.First(&pending, "client_id = ? AND node_id = ?", created.ClientID, f.intent.NodeID).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := ProcessFleetPlacementIntent(t.Context(), f.f.db, pending); !errors.Is(err, want) {
				t.Fatalf("unprepared inherited target error=%v want=%v", err, want)
			}
			if err := f.f.db.Model(&pending).Update("available_unix_milli", 0).Error; err != nil {
				t.Fatal(err)
			}
			if err := processFleetPlacementNode(t.Context(), f.f.db, f.intent.NodeID); err != nil {
				t.Fatalf("unprepared credential dependency stopped placement scheduler: %v", err)
			}
			if err := f.f.db.First(&pending, "id = ?", pending.ID).Error; err != nil || !pending.Pending || pending.MaterializedPlacementID != nil || pending.AvailableUnixMilli <= time.Now().UnixMilli() {
				t.Fatalf("unprepared target lost pending retry or gained application: %+v %v", pending, err)
			}
			var claims int64
			if err := f.f.db.Model(&model.FleetSourceCredentialClaim{}).Where("client_id = ?", created.ClientID).Count(&claims).Error; err != nil || claims != 1 {
				t.Fatalf("placement minted missing source credential claims: %d %v", claims, err)
			}
			for _, row := range []any{&model.FleetTerm{}, &model.FleetGrant{}, &model.FleetAuthorityUpdate{}} {
				var count int64
				if err := f.f.db.Model(row).Where("client_id = ?", created.ClientID).Count(&count).Error; err != nil || count != 0 {
					t.Fatalf("unprepared source acquired authority: %T %d %v", row, count, err)
				}
			}
		})
	}
}
