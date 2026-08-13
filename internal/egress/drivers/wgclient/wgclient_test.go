package wgclient

import (
	"encoding/json"
	"testing"

	"github.com/Arman2122/p-ui/internal/egress"
	engine "github.com/Arman2122/p-ui/internal/wireguard"
)

func row(t *testing.T, id int, s Settings) egress.Egress {
	t.Helper()
	raw, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	return egress.Egress{ID: id, Type: Type, Enable: true, Settings: raw}
}

func driver() Driver { return New(engine.GetUplinkManager()) }

/*
The device Fill routes to must be the device Provision creates.

Measured failure: Fill named pux4 while the engine created pwg4 from the id
alone. The row went healthy, the ip rule pointed at a device that did not exist,
and every packet for that egress fell through to the main table -- the tunnel
looked configured and changed nothing about where traffic left.
*/
func TestFillNamesTheDeviceTheEngineCreates(t *testing.T) {
	e := row(t, 4, Settings{Address: []string{"10.14.0.2/32"}, Endpoint: "example:51820"})

	fill, err := driver().Fill(e)
	if err != nil {
		t.Fatalf("Fill: %v", err)
	}
	if want := engine.GetUplinkManager().Name(4); fill.Device != want {
		t.Fatalf("Fill routes to %q but the engine creates %q", fill.Device, want)
	}
}

/*
An uplink must land in the egress namespace the manager sweeps by, or converge
treats the panel's own device as a stranger's and never reaps it.
*/
func TestUplinkDeviceIsInTheEgressNamespace(t *testing.T) {
	e := row(t, 7, Settings{Address: []string{"10.14.0.2/32"}, Endpoint: "example:51820"})

	fill, err := driver().Fill(e)
	if err != nil {
		t.Fatalf("Fill: %v", err)
	}
	if fill.Device != egress.Uplink(7) {
		t.Fatalf("device %q is outside the egress uplink namespace %q", fill.Device, egress.Uplink(7))
	}
}

/*
The collision this separation exists to prevent: inbound 4 and uplink 4 are
different rows in different tables, and one device cannot serve both.
*/
func TestUplinkAndInboundOfTheSameIDAreDifferentDevices(t *testing.T) {
	if got, other := engine.GetUplinkManager().Name(4), engine.InterfaceName(4); got == other {
		t.Fatalf("uplink 4 and inbound 4 both claim %q: two writers, one device", got)
	}
}

// A family the uplink holds no address in must not be routed into it: the kernel
// would borrow another interface's source and leak the host's own identity.
func TestFillCarriesOnlyTheFamiliesTheProviderAssigned(t *testing.T) {
	e := row(t, 2, Settings{Address: []string{"10.14.0.2/32"}, Endpoint: "example:51820"})

	fill, err := driver().Fill(e)
	if err != nil {
		t.Fatalf("Fill: %v", err)
	}
	if len(fill.Families) != 1 || fill.Families[0] != egress.FamilyV4 {
		t.Fatalf("families = %v, want v4 only for a v4-only address", fill.Families)
	}
}

// What reaches an uplink is a socket this host originated, which has no ingress
// device to match on. Unmarked, the rule can never select it.
func TestUplinkIsSelectedByMark(t *testing.T) {
	e := row(t, 3, Settings{Address: []string{"10.14.0.2/32"}, Endpoint: "example:51820"})

	fill, err := driver().Fill(e)
	if err != nil {
		t.Fatalf("Fill: %v", err)
	}
	if !fill.Marked {
		t.Fatal("an uplink carrying locally-originated traffic must be selected by fwmark")
	}
}

// An uplink with nowhere to dial is a refusal, not a device that quietly holds
// no peer and drops everything routed into it.
func TestProvisionRefusesAnUplinkWithNoEndpoint(t *testing.T) {
	e := row(t, 5, Settings{Address: []string{"10.14.0.2/32"}})

	if err := driver().Provision(t.Context(), e); err == nil {
		t.Fatal("an uplink with no endpoint must be refused, not created empty")
	}
}
