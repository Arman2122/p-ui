package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/routing"
)

/*
An uplink whose own settings stopped resolving must not keep being routed to.

Two layers failed open independently, and both had to. validate() returned
before reading Settings for anything that is not a front, so a typo in an
uplink's Address saved cleanly. Exits() then kept the row, because
wireguard.Core.ExitHandle is unconditional — it answers with a device name
without ever reading the settings — so the planner emitted a marked outbound.
Meanwhile the egress manager caught the driver's Fill error, ZEROED the fill and
carried on, so fill.Marked was false and the fwmark rule was never installed.

Marked packets with no rule to catch them fall through to the main table: every
client on the rules pointing at that uplink egresses with the server's own
public address, while the panel shows the rule as fine and the pattern chip as
"marked". An identity leak with no error anywhere.
*/
func TestAnUplinkTheDriverCannotRealiseIsRefusedAtSave(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)

	// A wg-client uplink with an Address the driver cannot parse. Everything
	// else about the row is well formed, which is the point: only Fill knows.
	_, err := (&EgressService{}).Add(&model.Egress{
		Type: "wg-client", Enable: true, Owner: model.EgressOwnerOperator,
		Remark:   "office",
		Settings: `{"address":["not-an-address"],"privateKey":"","peer":{}}`,
	})
	if err == nil {
		t.Fatal("saved an uplink its own driver cannot realise; the rules pointing at it leak the server's address")
	}
	if !errors.Is(err, ErrEgressUnrealisable) {
		t.Fatalf("error %v is not %v, so nothing tells the operator what to fix", err, ErrEgressUnrealisable)
	}
}

/*
The second layer, for a row that predates the first: it is already stored, so
the save-time check never ran on it. The planner must still not route to it.
*/
func TestAStoredUplinkThatStoppedResolvingIsNotRoutable(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)

	// Written straight to the table, as an upgrade would leave it.
	broken := seedEgress(t, &model.Egress{
		Type: "wg-client", Enable: true, Owner: model.EgressOwnerOperator,
		Remark:   "office",
		Settings: `{"address":["not-an-address"]}`,
	})

	exits, err := (&RoutingService{}).Exits(context.Background())
	if err != nil {
		t.Fatalf("Exits: %v", err)
	}
	for _, exit := range exits {
		if exit.ID == broken.Id {
			t.Fatalf("exit %d is offered as routable; the plan emits a marked outbound and the manager installs no rule for it", broken.Id)
		}
	}
}

/*
And the rule that names it is refused rather than compiled into a mark with no
rule behind it — which is what "fails closed" has to mean at plan time.
*/
func TestARuleNamingAnUnrealisableUplinkIsRefused(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)
	installCores(t, newProxyCore(&declaredCore{id: "proxy", kind: "vless"}))
	inbound := seedPolicyInbound(t, "vless", "vless-exit", 45711, "client")

	broken := seedEgress(t, &model.Egress{
		Type: "wg-client", Enable: true, Owner: model.EgressOwnerOperator,
		Remark: "office", Settings: `{"address":["not-an-address"]}`,
	})

	_, err := (&RoutingService{}).Add(context.Background(), &model.RoutingRule{
		Enable: true, IngressScope: model.RoutingScopeSelected,
		IngressIds: ingressIdsJSON(t, inbound.Id),
		DestKind:   model.RoutingDestExit, DestExitId: &broken.Id,
	})
	if err == nil {
		t.Fatal("a rule pointing at an unrealisable uplink was stored; its clients egress with the server's own address")
	}
	var refusal *RefusalError
	if !errors.As(err, &refusal) {
		t.Fatalf("error %v is not a refusal", err)
	}
	if refusal.Key != routing.KeyExitMissing {
		t.Fatalf("refusal key %q, want %q", refusal.Key, routing.KeyExitMissing)
	}
}

// A well-formed uplink still saves and is still routable, or the guard is a wall.
func TestAWorkingUplinkIsUnaffected(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)

	row, err := (&EgressService{}).Add(&model.Egress{
		Type: "wg-client", Enable: true, Owner: model.EgressOwnerOperator,
		Remark:   "office",
		Settings: workingUplinkSettings(t),
	})
	if err != nil {
		t.Fatalf("a well-formed uplink was refused: %v", err)
	}

	exits, err := (&RoutingService{}).Exits(context.Background())
	if err != nil {
		t.Fatalf("Exits: %v", err)
	}
	var found bool
	for _, exit := range exits {
		if exit.ID == row.Id {
			found = true
			if exit.Kind != core.ExitDevice {
				t.Fatalf("exit kind %q, want a device exit", exit.Kind)
			}
		}
	}
	if !found {
		t.Fatal("a working uplink is no longer offered as a routing destination")
	}
}

func workingUplinkSettings(t *testing.T) string {
	t.Helper()
	return `{"address":["10.9.0.2/32"],"secretKey":"iD8eFDAR8KSbAAytwnhrggL20b49Kq88VJBVluGR83M=",` +
		`"peer":{"publicKey":"dHn/ghBAcQm2cUthFC2V4Uof/X3eAGwHEEPOtGNzcTc=","endpoint":"203.0.113.9:51820",` +
		`"allowedIPs":["0.0.0.0/0"]}}`
}

var _ = strings.TrimSpace
