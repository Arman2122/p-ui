package service

import (
	"encoding/json"
	"errors"
	"net/netip"
	"slices"
	"testing"
)

func TestFleetNetworkDefaultConflictRemainsEditable(t *testing.T) {
	f := fleetFixture(t, false)
	service, err := NewFleetNetworkService(f.db)
	if err != nil {
		t.Fatal(err)
	}
	initial := FleetNetworkSpec{
		Version: 1,
		Bases:   map[string]FleetRuntimeBase{"xray": {Core: "xray", Configuration: json.RawMessage(`{"outbounds":[{"tag":"direct","protocol":"freedom"}]}`)}},
		Bridges: map[string]FleetNetworkBridge{"bridge": {RuntimeID: "xray", Core: "xray", Endpoint: netip.MustParseAddrPort("127.0.0.1:2081")}},
	}
	if _, err := service.ApproveDefaults(t.Context(), &f.admin, 0, initial); err != nil {
		t.Fatal(err)
	}
	request := FleetNodeNetworkRequest{
		NodeID: f.nodes[0].NodeID, InstallationID: f.nodes[0].InstallationID,
		Operations: []FleetNetworkOperation{{Section: "inbound", InboundID: f.inbounds[0].Id, Operation: "replace", Inbound: &FleetInboundNetwork{EgressMode: "xray", BridgeID: "bridge"}}},
	}
	if _, err := service.ConfigureNode(t.Context(), &f.admin, request); err != nil {
		t.Fatal(err)
	}
	defaults, err := service.ApproveDefaults(t.Context(), &f.admin, 1, FleetNetworkSpec{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	state, err := service.ReadNode(t.Context(), &f.admin, request.NodeID)
	if err != nil || !state.Configured || state.Revision.Revision != 1 || state.Defaults.ID != defaults.ID || len(state.Overrides.Inbounds) != 1 ||
		!slices.ContainsFunc(state.Diagnostics, func(d FleetNetworkDiagnostic) bool { return d.Code == "NETWORK_OVERRIDE_CONFLICT" }) {
		t.Fatalf("default conflict hid editable state=%+v err=%v", state, err)
	}
	if _, err := service.Preview(t.Context(), &f.admin, FleetNetworkPreviewRequest{NodeID: request.NodeID, InstallationID: request.InstallationID, ExpectedRevision: 1, ExpectedDefaultsID: defaults.ID}); !errors.Is(err, ErrFleetNetworkDependency) {
		t.Fatalf("unresolved override escaped preview gate=%v", err)
	}
	request.ExpectedRevision = 1
	request.Operations = []FleetNetworkOperation{{Section: "inbound", InboundID: f.inbounds[0].Id, Operation: "reset"}}
	if _, err := service.ConfigureNode(t.Context(), &f.admin, request); err != nil {
		t.Fatal(err)
	}
	state, err = service.ReadNode(t.Context(), &f.admin, request.NodeID)
	if err != nil || len(state.Diagnostics) != 0 || len(state.Overrides.Inbounds) != 0 || state.Revision.Revision != 2 {
		t.Fatalf("explicit reset failed to resolve default conflict=%+v err=%v", state, err)
	}
}
