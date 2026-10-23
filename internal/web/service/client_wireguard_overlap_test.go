package service

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestDefaultWireguardClientsRejectsOverlappingAllowedIPs(t *testing.T) {
	existing := []model.Client{{Email: "a@wg", AllowedIPs: []string{"10.10.2.9/24"}}}
	clients := []model.Client{{Email: "b@wg", AllowedIPs: []string{"10.10.2.52/24"}}}
	err := defaultWireguardClients(existing, clients, []any{map[string]any{"email": "b@wg"}}, wireguardPool{base: "10.10.2.0/24"})
	const want = "wireguard: allowedIPs entry already used by another client: 10.10.2.52/24\n"
	if err == nil || err.Error() != want {
		t.Fatalf("overlapping allowedIPs error = %v, want %q", err, want)
	}
}

func TestAllocateWireguardAddressSkipsUsedPrefixes(t *testing.T) {
	got, err := allocateWireguardAddress([]string{"10.0.0.2/31"}, "10.0.0.0/24", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "10.0.0.4/32" {
		t.Fatalf("allocated %q, want 10.0.0.4/32", got)
	}
}
