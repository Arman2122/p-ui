package service

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/egress"
)

/*
A tunnel inbound with no destination forwards to itself.

Measured against the pinned core: with followRedirect off, dokodemo takes
rewriteAddress and substitutes 127.0.0.1 where it is nil, then takes rewritePort
and substitutes THE CONNECTION'S OWN LOCAL PORT where it is 0. So an inbound
with neither dials itself, and every connection it accepts opens another --
from one external probe, until the file descriptors of the xray process serving
every other inbound on the box are gone.

The panel shipped that shape as its default. F316.
*/
const selfDialSettings = `{"portMap":{},"allowedNetwork":"tcp,udp","followRedirect":false}`

func TestATunnelInboundThatCanOnlyDialItselfIsRefused(t *testing.T) {
	setupConflictDB(t)

	_, _, _, err := (&InboundService{}).AddInbound(&model.Inbound{
		Tag: "tunnel-loop", Enable: true, Port: 47301,
		Protocol: model.Tunnel, Settings: selfDialSettings,
	})
	if err == nil {
		t.Fatal("an inbound that forwards to itself was accepted; one external probe exhausts the core's file descriptors")
	}
	if !strings.Contains(err.Error(), "forwards to itself") {
		t.Fatalf("the refusal does not say what is wrong: %v", err)
	}
}

// Every way out of the loop is accepted, because each one gives the connection
// somewhere else to go.
func TestATunnelInboundWithADestinationIsAccepted(t *testing.T) {
	for _, tc := range []struct {
		name     string
		settings string
	}{
		{"a rewrite address", `{"rewriteAddress":"10.0.0.9","followRedirect":false}`},
		{"a rewrite port", `{"rewritePort":8053,"followRedirect":false}`},
		{"a port map", `{"portMap":{"443":"10.0.0.9:443"},"followRedirect":false}`},
		{"follow redirect", `{"followRedirect":true}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupConflictDB(t)
			_, _, _, err := (&InboundService{}).AddInbound(&model.Inbound{
				Tag: "tunnel-ok", Enable: true, Port: 47302,
				Protocol: model.Tunnel, Settings: tc.settings,
			})
			if err != nil {
				t.Fatalf("a tunnel inbound with %s was refused: %v", tc.name, err)
			}
		})
	}
}

/*
And a stored inbound that already forwards to itself stays editable.

A refusal applied to rows that predate it locks the operator out of the row they
most need to reach: renaming the remark, or turning the thing OFF, would be
refused, and the only way out would be deleting it and its clients. The edit is
judged against what it replaces. Q1.
*/
func TestAnEditThatDoesNotIntroduceTheLoopIsAccepted(t *testing.T) {
	setupConflictDB(t)
	db := seedTunnelDirect(t, "tunnel-legacy", 47303, selfDialSettings)

	edit := *db
	edit.Remark = "renamed while still broken"
	if _, _, _, err := (&InboundService{}).UpdateInbound(&edit); err != nil {
		t.Fatalf("an unrelated edit to an already-looping inbound was refused, so the operator cannot rename or disable it: %v", err)
	}
}

// The other half: an edit that CREATES the loop is stopped.
func TestAnEditThatIntroducesTheLoopIsRefused(t *testing.T) {
	setupConflictDB(t)
	db := seedTunnelDirect(t, "tunnel-good", 47304, `{"rewriteAddress":"10.0.0.9","followRedirect":false}`)

	edit := *db
	edit.Settings = selfDialSettings
	_, _, _, err := (&InboundService{}).UpdateInbound(&edit)
	if err == nil {
		t.Fatal("an edit that removed the destination was accepted; the inbound now forwards to itself")
	}
	if !strings.Contains(err.Error(), "forwards to itself") {
		t.Fatalf("the refusal does not say what is wrong: %v", err)
	}
}

// Seeded straight into the table, because the point is a row the save path
// would refuse today.
func seedTunnelDirect(t *testing.T, tag string, port int, settings string) *model.Inbound {
	t.Helper()
	seedInboundConflict(t, tag, "", port, model.Tunnel, "", settings)
	return loadInboundByTag(t, tag)
}

/*
The rule reaches the host-address check, which create has always run and edit
never did.

A WireGuard prefix moved onto the host's LAN, docker's bridge or the panel's own
management network captures that traffic however it arrives -- but an inbound
already overlapping must stay editable, or the operator cannot rename it, move
it off the collision, or turn it off. F067 / Q1.
*/
func TestAnAlreadyOverlappingWireguardInboundStaysEditable(t *testing.T) {
	setupBulkDB(t)
	stubHostAddrs(t, nil, nil)

	created, _, _, err := (&InboundService{}).AddInbound(wgkernelInbound(51995, "10.66.0.1/24"))
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	// The host turns out to hold that subnet after the fact -- a NIC brought up,
	// a docker network created, a route added by something else on the box.
	stubHostAddrs(t, []egress.AddrSpec{hostAddr(t, "10.66.0.5/24", "eth0")}, nil)

	off := *created
	off.Enable = false
	if _, _, _, err := (&InboundService{}).UpdateInbound(&off); err != nil {
		t.Fatalf("an inbound whose subnet the host has since claimed could not be disabled: %v", err)
	}
}

// And an edit that MOVES an inbound onto a subnet the host holds is refused,
// which is the half create has always covered.
func TestAnEditOntoASubnetTheHostHoldsIsRefused(t *testing.T) {
	setupBulkDB(t)
	stubHostAddrs(t, []egress.AddrSpec{hostAddr(t, "10.77.0.5/24", "eth0")}, nil)

	created, _, _, err := (&InboundService{}).AddInbound(wgkernelInbound(51996, "10.88.0.1/24"))
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	moved := *created
	moved.Settings = strings.ReplaceAll(moved.Settings, "10.88.0.1/24", "10.77.0.1/24")
	if _, _, _, err := (&InboundService{}).UpdateInbound(&moved); err == nil {
		t.Fatal("an edit moved the device onto a subnet eth0 already carries; the connected route it installs captures that network's traffic")
	}
}
