package service

import (
	"bytes"
	"encoding/json"
	"encoding/pem"
	"errors"
	"os"
	"slices"
	"testing"

	"github.com/google/uuid"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func fleetNativePublicationFixture(t *testing.T, protocol model.Protocol) (fleetClientFixture, *FleetNetworkService, FleetNetworkPreview) {
	t.Helper()
	f := fleetFixture(t, false)
	f.client.Password = "native-test-password"
	if err := f.db.Model(&f.client).Update("password", f.client.Password).Error; err != nil {
		t.Fatal(err)
	}
	certificatePath, keyPath := writeTestCertificate(t)
	certificate, err := os.ReadFile(certificatePath)
	if err != nil {
		t.Fatal(err)
	}
	key, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{"subnet": "10.57.0.0/24", "serverCert": string(certificate), "serverKey": string(key), "dns": []string{"1.1.1.1"}, "redirectGateway": true}
	materials := map[string][]byte{"serverCert": certificate, "serverKey": key}
	kinds := map[string]string{"serverCert": "tls-certificate", "serverKey": "tls-private-key"}
	credentials := map[string]string{core.CredUsername: "native-user"}
	if protocol == model.OpenVPN {
		serverKey := pem.EncodeToMemory(&pem.Block{Type: "OpenVPN tls-crypt-v2 server key", Bytes: bytes.Repeat([]byte{7}, 128)})
		settings["proto"], settings["tlsCryptV2ServerKey"] = "udp", string(serverKey)
		materials["tlsCryptV2ServerKey"], kinds["tlsCryptV2ServerKey"] = serverKey, "openvpn-tls-crypt-v2-server"
		credentials[core.CredTLSCryptV2] = string(pem.EncodeToMemory(&pem.Block{Type: "OpenVPN tls-crypt-v2 client key", Bytes: bytes.Repeat([]byte{9}, 384)}))
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&model.Inbound{}).Where("id = ?", f.inbounds[0].Id).Updates(map[string]any{"protocol": protocol, "settings": string(encoded)}).Error; err != nil {
		t.Fatal(err)
	}
	for key, value := range credentials {
		if err := f.db.Create(&model.ClientCredential{ClientId: f.client.Id, InboundId: f.inbounds[0].Id, Key: key, Value: value}).Error; err != nil {
			t.Fatal(err)
		}
	}
	m, err := (FleetClientService{}).Materialize(t.Context(), &f.admin, FleetMaterializeRequest{PanelClientID: f.client.Id, NodeID: f.nodes[0].NodeID, InstallationID: f.nodes[0].InstallationID, RuntimeID: "native", CoreID: "native", InboundIDs: []int{f.inbounds[0].Id}})
	if err != nil {
		t.Fatal(err)
	}
	f.activate(t, f.approve(t, m))
	var journals []model.FleetRuntimeJournal
	if err := f.db.Where("node_id = ?", f.nodes[0].NodeID).Find(&journals).Error; err != nil {
		t.Fatal(err)
	}
	registration := &nodev1.JournalRegistration{NodeId: f.nodes[0].NodeID, InstallationId: f.nodes[0].InstallationID, AgentStateJournalId: f.hello(t, f.nodes[0]).AgentStateJournalId}
	for _, journal := range journals {
		registration.Runtimes = append(registration.Runtimes, &nodev1.RuntimeJournalIdentity{RuntimeId: journal.RuntimeID, CoreId: journal.CoreID, StateJournalId: journal.StateJournalID, CreditJournalId: journal.CreditJournalID})
	}
	registration.Runtimes = append(registration.Runtimes, &nodev1.RuntimeJournalIdentity{RuntimeId: "native", CoreId: "native", StateJournalId: uuid.NewString(), CreditJournalId: uuid.NewString()})
	if _, err := f.store.RegisterJournals(t.Context(), f.nodes[0], registration); err != nil {
		t.Fatal(err)
	}
	s, err := NewFleetNetworkService(f.db)
	if err != nil {
		t.Fatal(err)
	}
	slots := map[string]string{}
	fields := map[string]string{}
	var operations []FleetNetworkOperation
	for field, material := range materials {
		asset, err := s.PutAsset(t.Context(), &f.admin, FleetNetworkAssetRequest{AssetID: uuid.NewString(), NodeID: f.nodes[0].NodeID, InstallationID: f.nodes[0].InstallationID, Kind: kinds[field], Data: material})
		if err != nil {
			t.Fatal(err)
		}
		slots[field], fields[field] = kinds[field], field
		operations = append(operations, FleetNetworkOperation{Section: "asset-binding", Key: field, Operation: "replace", Asset: &FleetAssetReference{AssetID: asset.AssetID, Revision: asset.Revision}})
	}
	defaults, err := s.ApproveDefaults(t.Context(), &f.admin, 0, FleetNetworkSpec{Version: 1, AssetSlots: slots, Inbounds: map[int]FleetInboundNetwork{f.inbounds[0].Id: {Assets: fields}}})
	if err != nil {
		t.Fatal(err)
	}
	node, err := s.ConfigureNode(t.Context(), &f.admin, FleetNodeNetworkRequest{NodeID: f.nodes[0].NodeID, InstallationID: f.nodes[0].InstallationID, Operations: operations})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.Preview(t.Context(), &f.admin, FleetNetworkPreviewRequest{NodeID: node.NodeID, InstallationID: node.InstallationID, ExpectedRevision: node.Revision.Revision, ExpectedDefaultsID: defaults.ID})
	if err != nil || len(preview.NativeBindings) != 1 || len(preview.Runtimes) != 1 || len(preview.Diagnostics) != 1 || preview.Diagnostics[0].Code != "AUTHORITY_FUNDING_REQUIRED" {
		t.Fatalf("native preview=%+v %v", preview, err)
	}
	return f, s, preview
}

func TestFleetNativePublicationCommitsAddressesWithDeployment(t *testing.T) {
	for _, protocol := range []model.Protocol{model.OpenVPN, model.OCServ} {
		t.Run(string(protocol), func(t *testing.T) {
			f, s, preview := fleetNativePublicationFixture(t, protocol)
			request := FleetNetworkPublishRequest{OperationID: uuid.NewString(), CompilationID: preview.ID}
			funding := []FleetNetworkFunding{{PlacementID: preview.NativeBindings[0].PlacementID, Bytes: 100, Rates: controlplane.Rates{Upload: limits.Rate{Mode: limits.Limited, BitsPerSecond: 1000000, BurstBytes: 65536}, Download: limits.Rate{Mode: limits.Limited, BitsPerSecond: 2000000, BurstBytes: 65536}}}}
			for _, statement := range []string{
				`CREATE FUNCTION reject_native_commit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'native commit fault'; END $$`,
				`CREATE TRIGGER reject_native_commit BEFORE INSERT ON fleet_network_publications FOR EACH ROW EXECUTE FUNCTION reject_native_commit()`,
			} {
				if err := f.db.Exec(statement).Error; err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.PublishFunded(t.Context(), &f.admin, request, funding); err == nil || !bytes.Contains([]byte(err.Error()), []byte("native commit fault")) {
				t.Fatalf("publication fault not reached: %v", err)
			}
			for _, table := range []string{"fleet_native_bindings", "fleet_grants", "fleet_deployments"} {
				var count int64
				if err := f.db.Table(table).Count(&count).Error; err != nil || count != 0 {
					t.Fatalf("publication leaked %s=%d %v", table, count, err)
				}
			}
			if err := f.db.Exec(`DROP TRIGGER reject_native_commit ON fleet_network_publications`).Error; err != nil {
				t.Fatal(err)
			}
			published, err := s.PublishFunded(t.Context(), &f.admin, request, funding)
			if err != nil || published.DeploymentRevision != 1 {
				t.Fatalf("native publication=%+v %v", published, err)
			}
			var rows []model.FleetNativeBinding
			if err := f.db.Find(&rows).Error; err != nil || len(rows) != 1 || rows[0].ID != preview.NativeBindings[0].ID || rows[0].PublishedRevision != 1 || rows[0].Prefix != preview.NativeBindings[0].Prefix.String() {
				t.Fatalf("published address=%+v %v", rows, err)
			}
			replay, err := s.PublishFunded(t.Context(), &f.admin, request, funding)
			if err != nil || !replay.Replayed || replay.DeploymentRevision != published.DeploymentRevision {
				t.Fatalf("address publication replay=%+v %v", replay, err)
			}
			changed := slices.Clone(preview.NativeBindings)
			changed[0].TargetID++
			if err := persistFleetNativeBindings(f.db, model.FleetNode{ID: f.nodes[0].NodeID, InstallationID: f.nodes[0].InstallationID}, 2, changed); !errors.Is(err, ErrFleetNativeBinding) {
				t.Fatalf("published address relabeled=%v", err)
			}
		})
	}
}
