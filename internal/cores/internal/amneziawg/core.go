// Package amneziawg adapts the AmneziaWG kernel module to the core contract.
//
// Deliberately thin: AmneziaWG is kernel WireGuard plus obfuscation, so the
// device, peer and traffic handling is the wgkernel core reused with a
// different kind and a manager pointed at the other netlink family. Only what
// is genuinely different lives here.
package amneziawg

import (
	"context"

	"github.com/Arman2122/p-ui/internal/awg"
	"github.com/Arman2122/p-ui/internal/core"
	wgcore "github.com/Arman2122/p-ui/internal/cores/internal/wireguard"
	engine "github.com/Arman2122/p-ui/internal/wireguard"
)

// Kind is the protocol this core serves, distinct from wgkernel because a
// client of one cannot speak to the other: the obfuscation changes the wire.
const Kind core.Kind = "awgkernel"

// DevicePrefix is this core's own device namespace, so an AmneziaWG inbound and
// a WireGuard inbound of the same id are two devices rather than one contested
// name. Registered with internal/shaping so nothing else may claim it.
const DevicePrefix = "pawg"

// Core is the AmneziaWG adapter.
type Core struct {
	*wgcore.Core
}

// New returns a core over its own device manager: the AmneziaWG plane, under
// this core's prefix.
func New() *Core {
	mgr := engine.NewNamedManager(awg.NewPlane(), DevicePrefix)
	return &Core{Core: wgcore.NewFor(Kind, mgr)}
}

// Describe reuses the WireGuard core's capability claims -- they are the same
// protocol -- and replaces only what names this core in the UI.
func (c *Core) Describe() core.Descriptor {
	d := c.Core.Describe()
	d.TitleKey = "cores.awgkernel.title"
	return d
}

/*
Preflight answers the question that actually decides whether this core can run:
is the module there.

Distinct from the WireGuard core's, which asks about a different module. A
failure disables this core alone -- an operator on a host that cannot build the
module keeps every other protocol, and sees one honest reason rather than an
inbound that will not start.
*/
func (c *Core) Preflight(ctx context.Context) error {
	return awg.Driver{}.Probe()
}
