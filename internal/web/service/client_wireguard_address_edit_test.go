package service

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

// wgSettings renders the half of a wgkernel settings blob this guard reads.
func wgSettings(t *testing.T, addresses []string, clientAddrs ...string) string {
	t.Helper()
	clients := make([]map[string]any, 0, len(clientAddrs))
	for i, addr := range clientAddrs {
		clients = append(clients, map[string]any{
			"email":      "c" + string(rune('a'+i)) + "@wgk",
			"allowedIPs": []string{addr},
		})
	}
	blob, err := json.Marshal(map[string]any{"address": addresses, "clients": clients})
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	return string(blob)
}

/*
TestAnAddressEditCannotStrandTheClientsItAllocated.

Rebasing or narrowing an interface address fails INVISIBLY without this:
desiredRoutes installs a per-peer route for everything the device no longer
covers, so every client keeps working while the addressing plan stops meaning
anything and the .conf files already on their devices no longer match.
*/
func TestAnAddressEditCannotStrandTheClientsItAllocated(t *testing.T) {
	tests := []struct {
		name    string
		before  []string
		after   []string
		clients []string
		refuse  bool
	}{
		{
			name:   "a rebase moves the subnet out from under every client",
			before: []string{"10.90.0.1/24"}, after: []string{"10.91.0.1/24"},
			clients: []string{"10.90.0.2/32"}, refuse: true,
		},
		{
			name:   "narrowing drops the half its clients are in",
			before: []string{"10.90.0.1/16"}, after: []string{"10.90.0.1/24"},
			clients: []string{"10.90.7.9/32"}, refuse: true,
		},
		{
			name:   "widening is always safe",
			before: []string{"10.90.0.1/24"}, after: []string{"10.90.0.1/22"},
			clients: []string{"10.90.0.2/32"},
		},
		{
			name:   "narrowing past nobody is allowed",
			before: []string{"10.90.0.1/16"}, after: []string{"10.90.0.1/24"},
			clients: []string{"10.90.0.2/32"},
		},
		{
			name:   "removing one address of several keeps the clients the rest cover",
			before: []string{"10.90.0.1/24", "10.91.0.1/24"}, after: []string{"10.90.0.1/24"},
			clients: []string{"10.90.0.2/32"},
		},
		{
			name:   "removing the address a client sits inside is refused",
			before: []string{"10.90.0.1/24", "10.91.0.1/24"}, after: []string{"10.90.0.1/24"},
			clients: []string{"10.91.0.2/32"}, refuse: true,
		},
		{
			// The exemption that makes this shippable: a pool that widened past its
			// own /24 has clients outside it already, and holding an edit hostage to
			// those would refuse edits that fix nothing.
			name:   "a client already outside the old prefix does not block the edit",
			before: []string{"10.90.0.1/24"}, after: []string{"10.91.0.1/24"},
			clients: []string{"10.90.1.7/32"},
		},
		{
			name:   "an inbound with no clients can be re-addressed freely",
			before: []string{"10.90.0.1/24"}, after: []string{"10.91.0.1/24"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			old := &model.Inbound{Protocol: model.WGKernel, Settings: wgSettings(t, tc.before)}
			next := &model.Inbound{
				Protocol: model.WGKernel,
				Settings: wgSettings(t, tc.after, tc.clients...),
			}
			err := checkWireguardAddressCoverage(old, next)
			if tc.refuse && err == nil {
				t.Fatalf("the edit was allowed; %v is inside %v and outside %v, so its .conf no longer relates to the interface", tc.clients, tc.before, tc.after)
			}
			if !tc.refuse && err != nil {
				t.Fatalf("the edit was refused: %v", err)
			}
			if tc.refuse && !strings.Contains(err.Error(), tc.clients[0]) {
				t.Fatalf("the refusal must name the client it would strand, got %v", err)
			}
		})
	}
}

// TestOnlyAKernelDeviceIsChecked: an Xray wireguard inbound tunnels in userspace
// and installs no host route, so its addresses are nobody else's business.
func TestOnlyAKernelDeviceIsChecked(t *testing.T) {
	old := &model.Inbound{Protocol: model.WireGuard, Settings: wgSettings(t, []string{"10.90.0.1/24"})}
	next := &model.Inbound{
		Protocol: model.WireGuard,
		Settings: wgSettings(t, []string{"10.91.0.1/24"}, "10.90.0.2/32"),
	}
	if err := checkWireguardAddressCoverage(old, next); err != nil {
		t.Fatalf("a userspace wireguard inbound was refused: %v", err)
	}
}

/*
TestUpdateInboundRefusesAnAddressEditThatStrandsClients pins the WIRING.

The guard is only worth anything if the write path calls it, and settings.address
has exactly one: POST /panel/api/inbounds/update/:id lands in UpdateInbound. A
helper that refuses correctly and is never reached refuses nothing.
*/
func TestUpdateInboundRefusesAnAddressEditThatStrandsClients(t *testing.T) {
	setupBulkDB(t)
	svc := &InboundService{}
	ib := mkInbound(t, 51971, model.WGKernel, wgSettings(t, []string{"10.90.0.1/24"}, "10.90.0.2/32"))

	rebase := *ib
	rebase.Settings = wgSettings(t, []string{"10.91.0.1/24"}, "10.90.0.2/32")
	_, _, err := svc.UpdateInbound(&rebase)
	if err == nil {
		t.Fatal("the rebase was accepted; its client keeps working on a per-peer route while its .conf names an address the interface no longer has")
	}
	if !strings.Contains(err.Error(), "10.90.0.2/32") {
		t.Fatalf("the refusal must name the stranded client, got %v", err)
	}

	widen := *ib
	widen.Settings = wgSettings(t, []string{"10.90.0.1/22"}, "10.90.0.2/32")
	if _, _, err := svc.UpdateInbound(&widen); err != nil {
		t.Fatalf("widening strands nobody and must stay allowed: %v", err)
	}
}
