package service

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/netip"
	"sync"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func networkSpec(port uint16, inbound int) FleetNetworkSpec {
	return FleetNetworkSpec{
		Version: 1, Bases: map[string]FleetRuntimeBase{"xray": {Core: "xray", Configuration: json.RawMessage(`{"outbounds":[{"tag":"direct","protocol":"freedom"}]}`)}},
		Inbounds: map[int]FleetInboundNetwork{inbound: {Port: &port}}, AssetSlots: map[string]string{"certificate": "tls-certificate"},
	}
}

func TestFleetNetworkRevisionsFollowPinResetAndRejectStaleWrites(t *testing.T) {
	f := fleetFixture(t, false)
	service, err := NewFleetNetworkService(f.db)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.ApproveDefaults(t.Context(), &f.admin, 0, networkSpec(9000, f.inbounds[0].Id))
	if err != nil {
		t.Fatal(err)
	}
	port := uint16(9100)
	node, err := service.ConfigureNode(t.Context(), &f.admin, FleetNodeNetworkRequest{
		NodeID: f.nodes[0].NodeID, InstallationID: f.nodes[0].InstallationID,
		Operations: []FleetNetworkOperation{{Section: "inbound", InboundID: f.inbounds[0].Id, Operation: "replace", Inbound: &FleetInboundNetwork{Port: &port}}},
	})
	if err != nil || !node.FollowDefaults || node.Revision.Revision != 1 {
		t.Fatalf("following node=%+v err=%v", node, err)
	}
	follow := false
	pinned, err := service.ConfigureNode(t.Context(), &f.admin, FleetNodeNetworkRequest{NodeID: f.nodes[1].NodeID, InstallationID: f.nodes[1].InstallationID, FollowDefaults: &follow, PinnedDefaultsID: first.ID})
	if err != nil || pinned.FollowDefaults {
		t.Fatalf("pinned node=%+v err=%v", pinned, err)
	}
	second, err := service.ApproveDefaults(t.Context(), &f.admin, 1, networkSpec(9001, f.inbounds[0].Id))
	if err != nil {
		t.Fatal(err)
	}
	for index, want := range []string{second.ID, first.ID} {
		var head model.FleetNodeNetworkHead
		if err := f.db.First(&head, "node_id = ?", f.nodes[index].NodeID).Error; err != nil || head.DesiredDefaultsID != want || head.Revision != 1 || !head.Pending {
			t.Fatalf("default following silently forked or rewrote overrides=%+v err=%v", head, err)
		}
	}
	reset, err := service.ConfigureNode(t.Context(), &f.admin, FleetNodeNetworkRequest{
		NodeID: f.nodes[0].NodeID, InstallationID: f.nodes[0].InstallationID, ExpectedRevision: 1,
		Operations: []FleetNetworkOperation{{Section: "inbound", InboundID: f.inbounds[0].Id, Operation: "reset"}},
	})
	if err != nil || reset.Defaults.ID != second.ID || reset.Revision.Revision != 2 {
		t.Fatalf("reset failed to inherit current approved defaults=%+v err=%v", reset, err)
	}
	var row model.FleetNetworkRevision
	if err := f.db.First(&row, "id = ?", reset.Revision.ID).Error; err != nil {
		t.Fatal(err)
	}
	var overrides FleetNetworkOverrides
	if json.Unmarshal([]byte(row.Specification), &overrides) != nil || len(overrides.Inbounds) != 0 {
		t.Fatal("reset retained copied default or override")
	}
	var wg sync.WaitGroup
	var results [2]FleetNetworkRevisionView
	var failures [2]error
	for i := range results {
		wg.Go(func() {
			results[i], failures[i] = service.ApproveDefaults(t.Context(), &f.admin, 2, networkSpec(uint16(9200+i), f.inbounds[0].Id))
		})
	}
	wg.Wait()
	accepted, stale := 0, 0
	for i, result := range results {
		if failures[i] == nil && result.Revision == 3 {
			accepted++
		} else if errors.Is(failures[i], ErrFleetNetworkRevision) {
			stale++
		} else {
			t.Fatalf("concurrent defaults result=%+v err=%v", result, failures[i])
		}
	}
	if accepted != 1 || stale != 1 {
		t.Fatalf("lost optimistic default update: accepted=%d stale=%d", accepted, stale)
	}
	var preserved model.FleetNetworkRevision
	if err := f.db.First(&preserved, "id = ?", first.ID).Error; err != nil || hex.EncodeToString(preserved.Digest) != first.SHA256 || preserved.Revision != 1 {
		t.Fatalf("old network revision changed: %v", err)
	}
	exact := networkSpec(9500, f.inbounds[0].Id)
	base := exact.Bases["xray"]
	base.Configuration = json.RawMessage(`{"outbounds":[],"opaqueCounter":9007199254740993}`)
	exact.Bases["xray"] = base
	if _, err := service.ApproveDefaults(t.Context(), &f.admin, 3, exact); err != nil {
		t.Fatal(err)
	}
	read, err := service.ReadDefaults(t.Context(), &f.admin)
	if err != nil || !bytes.Contains(read.Specification.Bases["xray"].Configuration, []byte("9007199254740993")) {
		t.Fatalf("stored network configuration rounded an exact integer: %s err=%v", read.Specification.Bases["xray"].Configuration, err)
	}
}

func TestFleetNetworkAssetsStayNodeBoundAndReferencedRemovalsFail(t *testing.T) {
	f := fleetFixture(t, false)
	var trace bytes.Buffer
	db := f.db.Session(&gorm.Session{Logger: logger.New(log.New(&trace, "", 0), logger.Config{LogLevel: logger.Info})})
	service, err := NewFleetNetworkService(db)
	if err != nil {
		t.Fatal(err)
	}
	spec := networkSpec(9000, f.inbounds[0].Id)
	spec.Bridges = map[string]FleetNetworkBridge{"bridge": {RuntimeID: "xray", Core: "xray", Endpoint: netip.MustParseAddrPort("127.0.0.1:2081")}}
	binding := spec.Inbounds[f.inbounds[0].Id]
	binding.EgressMode, binding.BridgeID = "xray", "bridge"
	spec.Inbounds[f.inbounds[0].Id] = binding
	if _, err := service.ApproveDefaults(t.Context(), &f.admin, 0, spec); err != nil {
		t.Fatal(err)
	}
	secret := []byte("PRIVATE-ASSET-CONTENT-NOT-FOR-PREVIEWS")
	asset, err := service.PutAsset(t.Context(), &f.admin, FleetNetworkAssetRequest{AssetID: uuid.NewString(), NodeID: f.nodes[0].NodeID, InstallationID: f.nodes[0].InstallationID, Kind: "tls-certificate", Data: secret})
	if err != nil {
		t.Fatal(err)
	}
	if trace.Len() != 0 {
		t.Fatal("network asset write emitted SQL parameters")
	}
	op := FleetNetworkOperation{Section: "asset-binding", Key: "certificate", Operation: "replace", Asset: &FleetAssetReference{AssetID: asset.AssetID, Revision: 1}}
	if _, err := service.ConfigureNode(t.Context(), &f.admin, FleetNodeNetworkRequest{NodeID: f.nodes[1].NodeID, InstallationID: f.nodes[1].InstallationID, Operations: []FleetNetworkOperation{op}}); !errors.Is(err, ErrFleetNetworkDependency) {
		t.Fatalf("foreign node inherited concrete asset identity=%v", err)
	}
	request := FleetNodeNetworkRequest{NodeID: f.nodes[0].NodeID, InstallationID: f.nodes[0].InstallationID, Operations: []FleetNetworkOperation{op}}
	node, err := service.ConfigureNode(t.Context(), &f.admin, request)
	if err != nil {
		t.Fatal(err)
	}
	request.ExpectedRevision = node.Revision.Revision
	request.Operations = []FleetNetworkOperation{{Section: "bridge", Key: "bridge", Operation: "remove"}}
	if _, err := service.ConfigureNode(t.Context(), &f.admin, request); !errors.Is(err, ErrFleetNetworkDependency) {
		t.Fatalf("removed bridge still had inbound references=%v", err)
	}
	request.Operations = append(request.Operations, FleetNetworkOperation{Section: "inbound", InboundID: f.inbounds[0].Id, Operation: "replace", Inbound: &FleetInboundNetwork{EgressMode: "direct"}})
	if _, err := service.ConfigureNode(t.Context(), &f.admin, request); err != nil {
		t.Fatalf("atomic reference removal rejected=%v", err)
	}
	var stored model.FleetNetworkAssetRevision
	if err := f.db.First(&stored, "asset_id = ? AND revision = 1", asset.AssetID).Error; err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(stored)
	if err != nil || bytes.Contains(encoded, secret) || bytes.Contains(encoded, []byte(`"Data"`)) {
		t.Fatal("asset database record leaked payload during JSON serialization")
	}
	bad := networkSpec(9000, f.inbounds[0].Id)
	base := bad.Bases["xray"]
	for _, payload := range []string{`{"Clients":[{"email":"injected"}]}`, `{"clientſ":[{"email":"injected"}]}`} {
		base.Configuration = json.RawMessage(payload)
		bad.Bases["xray"] = base
		if _, err := service.ApproveDefaults(t.Context(), &f.admin, 1, bad); !errors.Is(err, ErrFleetNetwork) {
			t.Fatalf("operator network defaults injected governed clients=%v", err)
		}
	}
}
