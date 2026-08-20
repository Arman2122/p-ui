// Package amneziawg adapts the AmneziaWG kernel module to the core contract.
//
// Deliberately thin: AmneziaWG is kernel WireGuard plus obfuscation, so the
// device, peer and traffic handling is the wgkernel core reused with a
// different kind and a manager pointed at the other netlink family. Only what
// is genuinely different lives here.
package amneziawg

import (
	"context"
	"errors"
	"fmt"

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
	mgr *engine.Manager
}

// New returns a core over its own device manager: the AmneziaWG plane, under
// this core's prefix.
func New() *Core {
	mgr := engine.NewNamedManager(awg.NewPlane(), DevicePrefix)
	// Its own uplink manager too, so a dialled AmneziaWG device is named in this
	// module's namespace rather than kernel WireGuard's.
	return &Core{Core: wgcore.NewFor(Kind, mgr, awg.UplinkManager()), mgr: mgr}
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

/*
Reconcile and ApplyInstance apply the obfuscation after the device exists.

The device half goes through the WireGuard core untouched; the parameters cannot,
because wgtypes.Config has no room for them. Applying them here rather than
inside the Plane keeps AmneziaWG's vocabulary out of WireGuard's package -- and a
separate SET carrying only device attributes is merged by the module, so it does
not disturb a single peer.

Written only because the first end-to-end run proved it was missing: the panel
brought up a real pawg device with the right port and peers and NO obfuscation
at all, which is a tunnel every client configured for AmneziaWG fails to use
while the panel reports success.
*/
func (c *Core) Reconcile(ctx context.Context, desired []core.Instance) error {
	if err := c.Core.Reconcile(ctx, desired); err != nil {
		return err
	}
	var failures []error
	for _, inst := range desired {
		failures = append(failures, c.applyParams(ctx, inst))
	}
	return errors.Join(failures...)
}

func (c *Core) ApplyInstance(ctx context.Context, inst core.Instance) error {
	if err := c.Core.ApplyInstance(ctx, inst); err != nil {
		return err
	}
	return c.applyParams(ctx, inst)
}

// applyParams pushes one inbound's obfuscation onto its device. A device that is
// not there yet is not an error: the next pass makes it, and refusing here would
// fail a whole reconcile for an inbound still being created.
func (c *Core) applyParams(ctx context.Context, inst core.Instance) error {
	params, err := ParamsOf(inst)
	if err != nil {
		return err
	}
	if params.IsZero() {
		return nil
	}
	if err := awg.ConfigureParams(c.mgr.Name(inst.ID), params); err != nil {
		return fmt.Errorf("awgkernel: inbound %d obfuscation: %w", inst.ID, err)
	}
	return nil
}
