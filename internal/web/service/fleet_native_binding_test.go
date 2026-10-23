package service

import (
	"encoding/json"
	"errors"
	"net/netip"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores/nodeconfig"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/native"
)

func nativeBindingFixture(t *testing.T, kind core.Kind, subnet string) (fleetClientFixture, fleetNetworkState, nodeconfig.Input, FleetNetworkManifest) {
	t.Helper()
	f := fleetFixture(t, false)
	m := f.materialize(t, 0)
	m.Placement = f.approve(t, m)
	instance := uuid.NewSHA1(uuid.MustParse(f.nodes[0].InstallationID), []byte("native/inbound/"+strconv.Itoa(f.inbounds[0].Id))).String()
	input := nodeconfig.Input{NodeID: f.nodes[0].NodeID, Inbounds: []nodeconfig.Inbound{{Instance: core.Instance{ID: f.inbounds[0].Id, Kind: kind, Enable: true, Settings: `{"subnet":"` + subnet + `"}`}, TargetID: 1, TargetTag: "native-listener", Clients: []nodeconfig.Client{{ClientID: m.Client.ID}}}}}
	manifest := FleetNetworkManifest{NodeID: f.nodes[0].NodeID, InstallationID: f.nodes[0].InstallationID, Identities: []FleetNetworkAuthorityIdentity{{ClientID: m.Client.ID, PlacementID: m.Placement.ID, RuntimeID: "native", InboundTags: map[string]string{instance: "native-listener"}}}}
	state := fleetNetworkState{Node: model.FleetNode{ID: f.nodes[0].NodeID, InstallationID: f.nodes[0].InstallationID}}
	return f, state, input, manifest
}

func TestFleetNativeAddressPreviewIsStableAndCannotReuseAnotherOwner(t *testing.T) {
	f, state, input, manifest := nativeBindingFixture(t, "openvpn", "10.48.0.0/29")
	issues, err := prepareFleetNativeBindings(f.db, state, &input, &manifest)
	if err != nil || len(issues) != 0 || len(manifest.NativeBindings) != 1 {
		t.Fatalf("initial address plan=%+v %v", issues, err)
	}
	pin := manifest.NativeBindings[0]
	address := input.Inbounds[0].Clients[0].Native.Addresses[0]
	if address == netip.MustParseAddr("10.48.0.0") || address == netip.MustParseAddr("10.48.0.1") || address == netip.MustParseAddr("10.48.0.7") || pin.Prefix != netip.PrefixFrom(address, 32) {
		t.Fatalf("reserved server/network address assigned=%+v", pin)
	}
	var count int64
	if err := f.db.Model(&model.FleetNativeBinding{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("preview reserved addresses=%d %v", count, err)
	}
	if err := persistFleetNativeBindings(f.db, state.Node, 1, manifest.NativeBindings); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		input.Inbounds[0].Clients[0].Native = nodeconfig.NativeClientBinding{}
		manifest.NativeBindings = nil
		issues, err = prepareFleetNativeBindings(f.db, state, &input, &manifest)
		if err != nil || len(issues) != 0 || manifest.NativeBindings[0] != pin || input.Inbounds[0].Clients[0].Native.Addresses[0] != address {
			t.Fatalf("committed address changed=%+v %v", manifest.NativeBindings, err)
		}
	}
	other := uuid.NewString()
	identity := manifest.Identities[0]
	identity.ClientID, identity.PlacementID = other, uuid.NewString()
	manifest.Identities = append(manifest.Identities, identity)
	input.Inbounds[0].Clients = append(input.Inbounds[0].Clients, nodeconfig.Client{ClientID: other})
	manifest.NativeBindings = nil
	issues, err = prepareFleetNativeBindings(f.db, state, &input, &manifest)
	if err != nil || len(issues) != 0 || len(manifest.NativeBindings) != 2 {
		t.Fatalf("second client address=%+v %v", issues, err)
	}
	if manifest.NativeBindings[0].Prefix == manifest.NativeBindings[1].Prefix {
		t.Fatal("two identities acquired one address")
	}
}

func TestFleetNativePoolChangeRequiresAppliedRetirement(t *testing.T) {
	f, state, input, manifest := nativeBindingFixture(t, "ocserv", "10.49.0.0/28")
	issues, err := prepareFleetNativeBindings(f.db, state, &input, &manifest)
	if err != nil || len(issues) != 0 || len(manifest.NativeBindings) != 1 {
		t.Fatalf("session subnet=%+v %v", issues, err)
	}
	if manifest.NativeBindings[0].Prefix.Bits() != 29 || manifest.NativeBindings[0].MaxSessions != 1 {
		t.Fatalf("wrong session allocation=%+v", manifest.NativeBindings[0])
	}
	if err := persistFleetNativeBindings(f.db, state.Node, 1, manifest.NativeBindings); err != nil {
		t.Fatal(err)
	}
	input.Inbounds[0].Native.Subnet = netip.MustParsePrefix("10.50.0.0/28")
	manifest.NativeBindings = nil
	issues, err = prepareFleetNativeBindings(f.db, state, &input, &manifest)
	if err != nil || len(issues) != 1 || issues[0].Code != "NATIVE_BINDING_DRAIN_REQUIRED" {
		t.Fatalf("active address rebound without drain=%+v %v", issues, err)
	}
}

func TestNativeAddressPoolExhaustionNeverUsesReservedHosts(t *testing.T) {
	request := nodeconfig.NativeAddressRequest{Pool: netip.MustParsePrefix("10.51.0.0/29"), PrefixBits: 32, Reserved: []netip.Addr{netip.MustParseAddr("10.51.0.0"), netip.MustParseAddr("10.51.0.1")}}
	occupied := map[netip.Addr]string{}
	for _, address := range request.Reserved {
		occupied[address] = "server"
	}
	for range 5 {
		prefix, err := chooseNativePrefix(request, uuid.NewString(), occupied)
		if err != nil {
			t.Fatal(err)
		}
		occupied[prefix.Addr()] = "client"
	}
	if prefix, err := chooseNativePrefix(request, uuid.NewString(), occupied); !errors.Is(err, ErrFleetNativeBinding) || prefix.IsValid() {
		t.Fatalf("exhausted pool allocated=%v %v", prefix, err)
	}
}

func TestFleetNativeAddressAllocationRespectsWireGuardOwners(t *testing.T) {
	for name, values := range map[string]any{"strings": []string{"10.58.0.3/32"}, "decoded": []any{"10.58.0.3/32"}} {
		t.Run(name, func(t *testing.T) {
			f, state, input, manifest := nativeBindingFixture(t, "openvpn", "10.58.0.0/29")
			input.Inbounds = append(input.Inbounds, nodeconfig.Inbound{Instance: core.Instance{ID: 81, Kind: "wgkernel", Settings: `{"address":["10.58.0.2/24"]}`}, Clients: []nodeconfig.Client{{User: core.User{Credentials: map[string]any{core.CredAllowedIPs: values}}}}})
			issues, err := prepareFleetNativeBindings(f.db, state, &input, &manifest)
			if err != nil || len(issues) != 0 || len(manifest.NativeBindings) != 1 {
				t.Fatalf("mixed-native allocation=%+v %v", issues, err)
			}
			address := manifest.NativeBindings[0].Prefix.Addr()
			if address == netip.MustParseAddr("10.58.0.2") || address == netip.MustParseAddr("10.58.0.3") {
				t.Fatalf("address stolen from WireGuard=%v", address)
			}
			if err := persistFleetNativeBindings(f.db, state.Node, 1, manifest.NativeBindings); err != nil {
				t.Fatal(err)
			}
			input.Inbounds[1].Clients[0].Native.Addresses = []netip.Addr{address}
			manifest.NativeBindings = nil
			issues, err = prepareFleetNativeBindings(f.db, state, &input, &manifest)
			if err != nil || len(issues) != 1 || issues[0].Code != "NATIVE_ADDRESS_POOL_CONFLICT" {
				t.Fatalf("committed VPN address overwritten by WireGuard=%+v %v", issues, err)
			}
		})
	}
}

func TestNativeBindingAllocatorDoesNotModifyInputCredentials(t *testing.T) {
	f, state, input, manifest := nativeBindingFixture(t, "openvpn", "10.52.0.0/24")
	input.Inbounds[0].Clients[0].User.Credentials = map[string]any{"username": "stable-user", "password": "stable-password"}
	before, _ := json.Marshal(input.Inbounds[0].Clients[0].User)
	if issues, err := prepareFleetNativeBindings(f.db, state, &input, &manifest); err != nil || len(issues) != 0 {
		t.Fatalf("binding=%v %v", issues, err)
	}
	after, _ := json.Marshal(input.Inbounds[0].Clients[0].User)
	if string(before) != string(after) {
		t.Fatal("address allocation changed authentication credentials")
	}
}

func TestFleetNativeAddressAllocationReservesConfiguredShapingHop(t *testing.T) {
	f, state, input, manifest := nativeBindingFixture(t, "openvpn", "10.59.0.0/29")
	input.NativeNetwork = &native.NetworkBinding{PeerIPv4: netip.MustParseAddr("10.59.0.2"), GatewayIPv4: netip.MustParseAddr("10.59.0.3")}
	identity := manifest.Identities[0]
	for range 2 {
		identity.ClientID, identity.PlacementID = uuid.NewString(), uuid.NewString()
		manifest.Identities = append(manifest.Identities, identity)
		input.Inbounds[0].Clients = append(input.Inbounds[0].Clients, nodeconfig.Client{ClientID: identity.ClientID})
	}
	issues, err := prepareFleetNativeBindings(f.db, state, &input, &manifest)
	if err != nil || len(issues) != 0 || len(manifest.NativeBindings) != 3 {
		t.Fatalf("nonconflicting pool space unavailable=%+v %v", issues, err)
	}
	for _, pin := range manifest.NativeBindings {
		if pin.Prefix.Contains(input.NativeNetwork.PeerIPv4) || pin.Prefix.Contains(input.NativeNetwork.GatewayIPv4) {
			t.Fatalf("shaping hop assigned to client=%+v", pin)
		}
	}
	identity.ClientID, identity.PlacementID = uuid.NewString(), uuid.NewString()
	manifest.Identities = append(manifest.Identities, identity)
	input.Inbounds[0].Clients = append(input.Inbounds[0].Clients, nodeconfig.Client{ClientID: identity.ClientID})
	manifest.NativeBindings = nil
	issues, err = prepareFleetNativeBindings(f.db, state, &input, &manifest)
	if err != nil || len(issues) != 1 || issues[0].Code != "NATIVE_ADDRESS_POOL_EXHAUSTED" {
		t.Fatalf("shaping hop made available as extra client space=%+v %v", issues, err)
	}
}
