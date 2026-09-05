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
	"sync"

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
	// Kept alongside the manager so applyParams can ask whether a device exists.
	// The module cannot be asked: it answers a missing device and a refused
	// parameter set with the same errno.
	plane engine.Plane

	// What each device last accepted, keyed by inbound: the module is asked again
	// only when the device (its index) or the parameters differ. F270.
	appliedMu sync.Mutex
	applied   map[int]appliedParams
}

type appliedParams struct {
	index  int
	params awg.Params
}

// New returns a core over its own device manager: the AmneziaWG plane, under
// this core's prefix.
func New() *Core {
	plane := awg.NewPlane()
	mgr := engine.NewNamedManager(plane, DevicePrefix)
	// Its own uplink manager too, so a dialled AmneziaWG device is named in this
	// module's namespace rather than kernel WireGuard's.
	return &Core{Core: wgcore.NewFor(Kind, mgr, awg.UplinkManager()), mgr: mgr, plane: plane}
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
	/*
		Collected, not early-returned. The WireGuard core joins one error per
		inbound, so returning on it let a single unparseable sibling skip the
		obfuscation for every OTHER AmneziaWG inbound in the pass — and every
		subsequent pass took the same exit, so those devices stayed plain
		WireGuard while their clients were configured for AmneziaWG.
	*/
	failures := []error{c.Core.Reconcile(ctx, desired)}
	for _, inst := range desired {
		failures = append(failures, c.applyParams(ctx, inst))
	}
	return errors.Join(failures...)
}

/*
ValidateInstance refuses obfuscation the module would refuse, at the save.

awg.Params.Validate mirrors src/netlink.c rule for rule, and until now it was
reached only when the device was configured. The kernel answers a failed check
with EINVAL and a ratelimited dmesg line nobody reads, so the panel stored the
row, said success, and the inbound quietly did not work — while the .conf handed
to clients described the parameters the operator asked for.
*/
func (c *Core) ValidateInstance(_ core.Kind, settings string) error {
	params, err := ParamsOf(core.Instance{Settings: settings})
	if err != nil {
		return err
	}
	return params.Validate()
}

func (c *Core) ApplyInstance(ctx context.Context, inst core.Instance) error {
	if err := c.Core.ApplyInstance(ctx, inst); err != nil {
		return err
	}
	return c.applyParams(ctx, inst)
}

/*
applyParams pushes one inbound's obfuscation onto its device.

A device that is not there yet is genuinely not an error, and the module cannot
say so: it answers a missing device with EINVAL, the same errno it uses for a
parameter set it refuses (RUNTIME-RESULTS §10). So the presence question is
asked of the plane, which answers it without an error, and only a device that
exists is configured. Swallowing the errno instead would have swallowed the
refusal F095 exists to surface. Ledger L3.
*/
func (c *Core) applyParams(ctx context.Context, inst core.Instance) error {
	params, err := ParamsOf(inst)
	if err != nil {
		return err
	}
	name := c.mgr.Name(inst.ID)
	snap, err := c.plane.Snapshot(ctx, name)
	if err != nil {
		return fmt.Errorf("awgkernel: inbound %d device state: %w", inst.ID, err)
	}
	if !snap.Exists {
		return nil
	}
	key := appliedParams{index: snap.Link.Index, params: params}
	c.appliedMu.Lock()
	same := c.applied[inst.ID] == key
	c.appliedMu.Unlock()
	if same {
		return nil
	}
	// No IsZero() short-circuit: an inbound whose parameters were all cleared is
	// exactly the case that has to reach the device, and skipping it is half of
	// why a cleared one never did.
	if err := configureParams(name, params); err != nil {
		return fmt.Errorf("awgkernel: inbound %d obfuscation: %w", inst.ID, err)
	}
	c.appliedMu.Lock()
	if c.applied == nil {
		c.applied = map[int]appliedParams{}
	}
	c.applied[inst.ID] = key
	c.appliedMu.Unlock()
	return nil
}

// configureParams is the one call that reaches the module. A var so the
// conformance suite can drive this core against the fake kernel every other
// core is checked against; the value is never configurable. F093.
var configureParams = awg.ConfigureParams

// Installed is the AmneziaWG module's version, not WireGuard's.
func (c *Core) Installed(ctx context.Context) (string, error) {
	return wgcore.ModuleVersion(ctx, c.Preflight, "amneziawg")
}
