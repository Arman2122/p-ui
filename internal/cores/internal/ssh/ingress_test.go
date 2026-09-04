package ssh

import (
	"errors"
	"net"
	"slices"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

/*
An SSH inbound is offered to the routing editor only when its forwards
actually reach Xray, and is told why when they do not.

Offering the tag while the switch is off is the F060 shape: the rule saves,
the editor lists it as active, and no packet ever matches it.
*/
func TestIngressHandleAnswersOnlyWhenTheBridgeIsUp(t *testing.T) {
	c := New(Deps{})
	for _, tc := range []struct {
		name     string
		settings string
		wantTag  string
		wantKey  string
	}{
		{"switch off", `{"transport":"tcp"}`, "", blockedBridgeOff},
		{"switch on, no port yet", `{"transport":"tcp","routeThroughXray":true}`, "", blockedBridgeOff},
		{"switch on, port allocated", `{"transport":"tcp","routeThroughXray":true,"routeXrayPort":2401}`, "ssh-1", ""},
		{"unparsable settings", `{`, "", blockedBridgeOff},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handle, err := c.IngressHandle(t.Context(), core.Instance{ID: 1, Kind: Kind, Tag: "ssh-1", Settings: tc.settings})
			if err != nil {
				t.Fatalf("IngressHandle: %v", err)
			}
			if handle.Tag != tc.wantTag || handle.BlockedKey != tc.wantKey {
				t.Fatalf("handle = {tag %q blocked %q}, want {tag %q blocked %q}", handle.Tag, handle.BlockedKey, tc.wantTag, tc.wantKey)
			}
			if handle.Device != "" {
				t.Fatalf("an in-process core claimed a kernel device: %q", handle.Device)
			}
		})
	}
	if got := c.IngressSelector(Kind); got != core.IngressInternal {
		t.Fatalf("selector = %q, want internal", got)
	}
	if got := c.IngressSelector("vless"); got != core.IngressNone {
		t.Fatalf("this core answered for a kind it does not serve: %q", got)
	}
}

/*
The criteria are the ones a rule can actually match through the bridge.

Source and source port would save and never match: Xray reads a session's
source from the TCP peer, and the peer here is always the panel on loopback.
*/
func TestIngressCriteriaExcludeWhatTheBridgeCannotCarry(t *testing.T) {
	got := c().IngressCriteria(Kind)
	for _, want := range []core.Criterion{core.CriterionIP, core.CriterionPort, core.CriterionNetwork, core.CriterionDomain} {
		if !slices.Contains(got, want) {
			t.Errorf("criteria %v lack %q", got, want)
		}
	}
	for _, unwanted := range []core.Criterion{core.CriterionSource, core.CriterionSourcePort} {
		if slices.Contains(got, unwanted) {
			t.Errorf("criteria %v offer %q, which the bridge cannot carry", got, unwanted)
		}
	}
	if c().IngressCriteria("vless") != nil {
		t.Fatal("this core answered criteria for a kind it does not serve")
	}
}

func c() *Core { return New(Deps{}) }

/*
An inbound that is meant to leave through Xray never leaves any other way.

The window this covers is real and short: the switch is saved before the
bridge port is allocated. Dialling directly there would put the client's
traffic on the server's own address at the exact moment the operator asked for
it to leave somewhere else, and nothing would say so.
*/
func TestADialerIsChosenPerInboundAndFailsClosed(t *testing.T) {
	core := New(Deps{})
	off, err := parseSettings(`{"transport":"tcp"}`)
	if err != nil {
		t.Fatal(err)
	}
	if core.dialerFor(off) != nil {
		t.Fatal("an unrouted inbound was given a dialer instead of the default")
	}

	pending, err := parseSettings(`{"transport":"tcp","routeThroughXray":true}`)
	if err != nil {
		t.Fatal(err)
	}
	conn, dialErr := core.dialerFor(pending)(t.Context(), "tcp", "example.com:443")
	if dialErr == nil {
		_ = conn.Close()
		t.Fatal("a forward was dialled while the inbound had no bridge port; that dial leaves on the server's own address")
	}
	if !strings.Contains(dialErr.Error(), "bridge port") {
		t.Fatalf("the refusal does not say what is wrong: %v", dialErr)
	}

	// With a port, the dialer is the bridge: it reaches for loopback and fails
	// there because nothing is listening, rather than reaching the destination.
	ready, err := parseSettings(`{"transport":"tcp","routeThroughXray":true,"routeXrayPort":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.dialerFor(ready)(t.Context(), "tcp", "example.com:443"); err == nil {
		t.Fatal("the bridge dialer reached a destination with no bridge behind it")
	} else {
		var opErr *net.OpError
		if !errors.As(err, &opErr) || !strings.Contains(opErr.Error(), "127.0.0.1:1") {
			t.Fatalf("the dial did not go to the bridge on loopback: %v", err)
		}
	}
}

/*
Flipping the switch rebuilds the listener rather than hot-applying onto it.

The dialer is chosen when the server is built, so a hot apply would leave every
live forward on the old one and the operator would see the change take effect
only for clients that reconnect.
*/
func TestTheBridgeSwitchRestartsTheListener(t *testing.T) {
	before := core.Instance{ID: 1, Kind: Kind, Tag: "ssh-1", Port: 2222, Settings: `{"transport":"tcp"}`}
	after := before
	after.Settings = `{"transport":"tcp","routeThroughXray":true,"routeXrayPort":2401}`
	if got := New(Deps{}).PlanChange(before, after); got != core.ActionRestart {
		t.Fatalf("turning the bridge on planned %q, want a restart", got)
	}
	sameAgain := New(Deps{}).PlanChange(after, after)
	if sameAgain != core.ActionNoop {
		t.Fatalf("an unchanged inbound planned %q", sameAgain)
	}
}
