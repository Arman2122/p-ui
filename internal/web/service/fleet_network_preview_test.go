package service

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestFleetNetworkPreviewPinsSourcesWithoutFundingOrActivating(t *testing.T) {
	f := fleetFixture(t, false)
	placement := f.approve(t, f.materialize(t, 0))
	f.activate(t, placement)
	service, err := NewFleetNetworkService(f.db)
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := service.ApproveDefaults(t.Context(), &f.admin, 0, FleetNetworkSpec{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	node, err := service.ConfigureNode(t.Context(), &f.admin, FleetNodeNetworkRequest{NodeID: f.nodes[0].NodeID, InstallationID: f.nodes[0].InstallationID})
	if err != nil {
		t.Fatal(err)
	}
	request := FleetNetworkPreviewRequest{NodeID: f.nodes[0].NodeID, InstallationID: f.nodes[0].InstallationID, ExpectedRevision: node.Revision.Revision, ExpectedDefaultsID: defaults.ID}
	preview, err := service.Preview(t.Context(), &f.admin, request)
	if err != nil || !preview.Ready || len(preview.Runtimes) != 1 || preview.Runtimes[0].Clients != 1 || len(preview.Listeners) != 2 || len(preview.Identities) != 1 || preview.Identities[0].State != "authority_required" {
		t.Fatalf("active preview=%+v err=%v", preview, err)
	}
	encoded, err := json.Marshal(preview)
	if err != nil || bytes.Contains(encoded, []byte(f.client.UUID)) {
		t.Fatal("preview exposed protocol authentication material")
	}
	var grants int64
	if err := f.db.Model(&model.FleetGrant{}).Count(&grants).Error; err != nil || grants != 0 {
		t.Fatalf("preview issued forwarding authority=%d err=%v", grants, err)
	}
	dormant := model.ClientRecord{Email: "dormant-preview", UUID: uuid.NewString(), Enable: true, TotalGB: 1000, ExpiryTime: -3600000}
	if err := f.db.Create(&dormant).Error; err != nil {
		t.Fatal(err)
	}
	for _, inbound := range f.inbounds {
		if err := f.db.Create(&model.ClientInbound{ClientId: dormant.Id, InboundId: inbound.Id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := f.db.Create(&core.ClientTraffic{Email: dormant.Email, InboundId: f.inbounds[0].Id, Enable: true, Total: 1000, ExpiryTime: dormant.ExpiryTime}).Error; err != nil {
		t.Fatal(err)
	}
	other := f
	other.client = dormant
	other.approve(t, other.materialize(t, 0))
	blocked, err := service.Preview(t.Context(), &f.admin, request)
	if err != nil || !blocked.Ready || len(blocked.Runtimes) != 1 || blocked.Runtimes[0].Clients != 2 || !slices.ContainsFunc(blocked.Identities, func(identity FleetNetworkAuthorityIdentity) bool { return identity.State == "dormant" }) {
		t.Fatalf("qualified dormant definition lost identity or activated it=%+v err=%v", blocked, err)
	}
	var terms int64
	if err := f.db.Model(&model.FleetTerm{}).Count(&terms).Error; err != nil || terms != 1 {
		t.Fatalf("preview activated a dormant billing term=%d err=%v", terms, err)
	}
	if err := f.db.Model(&model.FleetGrant{}).Count(&grants).Error; err != nil || grants != 0 {
		t.Fatalf("dormant preview minted zero-credit grants=%d err=%v", grants, err)
	}
	if _, err := service.ApproveDefaults(t.Context(), &f.admin, 1, networkSpec(9001, f.inbounds[0].Id)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Preview(t.Context(), &f.admin, request); !errors.Is(err, ErrFleetNetworkRevision) {
		t.Fatalf("stale default snapshot preview=%v", err)
	}
}

func TestFleetNetworkPreviewRejectsMissingNativeAddressPool(t *testing.T) {
	f := fleetFixture(t, false)
	service, err := NewFleetNetworkService(f.db)
	if err != nil {
		t.Fatal(err)
	}
	defaults, err := service.ApproveDefaults(t.Context(), &f.admin, 0, FleetNetworkSpec{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	node, err := service.ConfigureNode(t.Context(), &f.admin, FleetNodeNetworkRequest{NodeID: f.nodes[0].NodeID, InstallationID: f.nodes[0].InstallationID})
	if err != nil {
		t.Fatal(err)
	}
	for _, protocol := range []model.Protocol{model.OpenVPN, model.OCServ} {
		if err := f.db.Model(&model.Inbound{}).Where("id = ?", f.inbounds[0].Id).Update("protocol", protocol).Error; err != nil {
			t.Fatal(err)
		}
		materialized, err := (FleetClientService{}).Materialize(t.Context(), &f.admin, FleetMaterializeRequest{
			PanelClientID: f.client.Id, NodeID: f.nodes[0].NodeID, InstallationID: f.nodes[0].InstallationID,
			RuntimeID: "native", CoreID: "native", InboundIDs: []int{f.inbounds[0].Id},
		})
		if err != nil {
			t.Fatal(err)
		}
		placement := f.approve(t, materialized)
		if protocol == model.OpenVPN {
			f.activate(t, placement)
		}
		preview, err := service.Preview(t.Context(), &f.admin, FleetNetworkPreviewRequest{NodeID: node.NodeID, InstallationID: node.InstallationID, ExpectedRevision: node.Revision.Revision, ExpectedDefaultsID: defaults.ID})
		if err != nil || preview.Ready || !slices.ContainsFunc(preview.Diagnostics, func(issue FleetNetworkDiagnostic) bool {
			return issue.Code == "NATIVE_ADDRESS_POOL_INVALID" && issue.InboundID == f.inbounds[0].Id
		}) {
			t.Fatalf("%s missing client binding diagnostic=%+v err=%v", protocol, preview, err)
		}
	}
}
