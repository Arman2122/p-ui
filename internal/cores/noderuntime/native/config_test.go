package nativeruntime

import (
	"encoding/json"
	"errors"
	"net/netip"
	"testing"

	"github.com/google/uuid"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func testConfig(t *testing.T) (Configuration, governor.Grant, wgtypes.Key) {
	t.Helper()
	server, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	client, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	grant := governor.Grant{ID: uuid.NewString(), ClientID: uuid.NewString(), Policy: limits.Policy{Term: uuid.NewString(), Revision: 1, Enabled: true, CreditCeiling: 64 << 20, Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}}}
	peer := Peer{ClientID: grant.ClientID, PublicKey: client.PublicKey().String(), Addresses: []netip.Addr{netip.MustParseAddr("10.239.1.2")}}
	inbound := WireGuard{ID: 1, Port: 31821, PrivateKey: server.String(), Addresses: []netip.Prefix{netip.MustParsePrefix("10.239.1.1/32")}, Clients: []Peer{peer}}
	config := Configuration{WireGuard: []WireGuard{inbound}}
	return config, grant, client
}

func asRuntime(t *testing.T, config Configuration, grants ...governor.Grant) *configuration.Runtime {
	t.Helper()
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	return &configuration.Runtime{ID: "native", Core: "native", Configuration: data, Grants: grants}
}

func TestWireGuardConfigurationRejectsAmbiguousAuthority(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Configuration, *governor.Grant)
	}{
		{"missing identity", func(c *Configuration, _ *governor.Grant) { c.WireGuard[0].Clients[0].ClientID = uuid.NewString() }},
		{"public tunnel", func(c *Configuration, _ *governor.Grant) {
			c.WireGuard[0].Clients[0].Addresses[0] = netip.MustParseAddr("1.1.1.1")
		}},
		{"connected subnet", func(c *Configuration, _ *governor.Grant) {
			c.WireGuard[0].Addresses[0] = netip.MustParsePrefix("10.239.1.1/24")
		}},
		{"duplicate credential", func(c *Configuration, g *governor.Grant) {
			c.WireGuard[0].Clients = append(c.WireGuard[0].Clients, c.WireGuard[0].Clients[0])
		}},
		{"server client address collision", func(c *Configuration, _ *governor.Grant) {
			c.WireGuard[0].Clients[0].Addresses[0] = c.WireGuard[0].Addresses[0].Addr()
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			config, grant, _ := testConfig(t)
			test.change(&config, &grant)
			if _, err := compile(asRuntime(t, config, grant)); !errors.Is(err, configuration.ErrConfiguration) {
				t.Fatalf("unsafe native configuration = %v", err)
			}
		})
	}
}
