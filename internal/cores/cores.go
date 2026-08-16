// Package cores is the one place every protocol core is wired into a registry.
package cores

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores/internal/mtproto"
	"github.com/Arman2122/p-ui/internal/cores/internal/wireguard"
	"github.com/Arman2122/p-ui/internal/cores/internal/xray"
	engine "github.com/Arman2122/p-ui/internal/xray"
)

// Deps are the panel-side facts a core cannot derive for itself. They are passed
// in rather than reached for, so a core still cannot import the web layer.
type Deps struct {
	// XrayBaseConfig returns the Xray config with every section except inbounds
	// filled in: routing, DNS and policy stay the panel's business.
	XrayBaseConfig func() (*engine.Config, error)
}

/*
Adding a core is one import and one Register line in this file.

Registration is explicit rather than init()-based on purpose. With init(), the
registered set is a function of the transitive import graph of whichever main
links the package — so a dropped blank import silently shrinks the generated
frontend schema while `make gen-check` still passes. It also keeps "which cores
exist" answerable by reading one file, and makes adding a core a reviewable
one-line diff.

Concrete cores live under internal/cores/internal/, so the Go compiler — not a
lint rule — stops the service layer from importing one directly.
*/

/*
ServedByXray reports whether the Xray core owns this kind, so the panel's Xray
config carries only inbounds xray-core can actually start.

Answered here rather than in the service layer because this package is the one
place already allowed to name a concrete core, and because the alternative — a
second registry over there — would build a second Xray adapter with its own
API client and its own pending tag deltas, which is how one measurement ends up
with two subjects.
*/
func ServedByXray(kind core.Kind) bool {
	bound, ok := kindOwners().For(kind)
	return ok && bound.Core.Describe().ID == xray.ID
}

// ClientCredentials names the credential fields one kind's clients carry, so a
// caller asks the registry instead of naming a protocol. Deps-free, as above.
func ClientCredentials(kind core.Kind) []string {
	bound, ok := kindOwners().For(kind)
	if !ok || bound.Creds == nil {
		return nil
	}
	return bound.Creds.ClientCredentials(kind)
}

/*
ClientShare renders what one client needs to connect, when their kind's core
can. host is the endpoint address the caller resolved — which hostname reaches
this panel is delivery policy, and no core learns it.

The second return distinguishes "this kind renders no share" from a render that
failed: the first is a normal state every URI-only kind is in today.
*/
func ClientShare(inst core.Instance, user core.User, host string) (core.Share, bool, error) {
	bound, ok := kindOwners().For(inst.Kind)
	if !ok || bound.Link == nil {
		return core.Share{}, false, nil
	}
	share, err := bound.Link.RenderClient(inst, user, host)
	if err != nil {
		return core.Share{}, true, err
	}
	return share, true, nil
}

// TransportAuthority is the core that answers what one kind binds, when any
// does. A kind no core claims has no authority, and the caller keeps its own
// oldest rule rather than inventing one here.
func TransportAuthority(kind core.Kind) (core.TransportDeclarer, bool) {
	bound, ok := kindOwners().For(kind)
	if !ok || bound.Transports == nil {
		return nil, false
	}
	return bound.Transports, true
}

// ClientCredentialAuthority is the core that answers for one kind's client
// credentials, when any does. The caller keeps its own fallback for a kind no
// core owns — a quarantined inbound's clients are neither loosened nor newly
// refused by this seam.
func ClientCredentialAuthority(kind core.Kind) (core.CredentialDeclarer, bool) {
	bound, ok := kindOwners().For(kind)
	if !ok || bound.Creds == nil {
		return nil, false
	}
	return bound.Creds, true
}

/*
kindOwners is the registry every facade helper answers from.

The one the panel WIRED, when it wired one: those are the adapter instances the
jobs drive and the supervisor restarts, and an answer from a second set of
instances is an answer about cores nothing is running — the facade and the jobs
used to disagree exactly that way. The deps-free build survives only as the
fallback for processes that never wire a runtime: the CLI, codegen, and tests
that call a facade helper directly.
*/
func kindOwners() *core.Registry {
	if reg := wiredRegistry.Load(); reg != nil {
		return reg
	}
	return fallbackRegistry()
}

// Use hands the facade the registry the panel actually runs. Called where the
// runtime manager is set, so the two can never name different instances.
func Use(reg *core.Registry) {
	if reg != nil {
		wiredRegistry.Store(reg)
	}
}

var wiredRegistry atomic.Pointer[core.Registry]

var fallbackRegistry = sync.OnceValue(func() *core.Registry {
	reg, err := Default(Deps{})
	if err != nil {
		panic("cores: " + err.Error())
	}
	return reg
})

/*
PanelConvergedCore is the one core the panel converges itself, through
GetXrayConfig, rather than through Supervisor.Reconcile.

Deps.XrayBaseConfig is not the whole of that config: subscription outbounds,
node egresses and the mtproto SOCKS bridges are injected after the inbound list,
so an instance set cannot describe it and reconciling from one would drop them.
Delete this, and the supervision job's skip, once the base config is complete.
*/
const PanelConvergedCore = xray.ID

// Register wires every core into reg. Cores land here as they are ported:
// mtproto first, because it is the smaller contract and proves the interface.
func Register(reg *core.Registry, deps Deps) error {
	if reg == nil {
		return fmt.Errorf("cores: Register(nil registry)")
	}
	if err := reg.Register(mtproto.New()); err != nil {
		return err
	}
	if err := reg.Register(wireguard.New()); err != nil {
		return err
	}
	return reg.Register(xray.New(xray.Deps{BaseConfig: deps.XrayBaseConfig}))
}

// Default builds the registry the panel runs with.
func Default(deps Deps) (*core.Registry, error) {
	reg := core.NewRegistry()
	if err := Register(reg, deps); err != nil {
		return nil, err
	}
	return reg, nil
}

/*
IngressSelectorFor answers how a kind's traffic is selected for routing.

Here rather than in the service layer for ServedByXray's reason: this package is
the one place allowed to name a concrete core, so the router asks the registry
and a new core becomes routable by registering, not by editing a switch.
*/
func IngressSelectorFor(kind core.Kind) core.IngressSelector {
	bound, ok := kindOwners().For(kind)
	if !ok || bound.Ingress == nil {
		return core.IngressNone
	}
	return bound.Ingress.IngressSelector(kind)
}

// IngressHandleFor resolves one instance's routable surface right now. A core
// without the capability answers an empty handle, which reads as "not routable".
func IngressHandleFor(ctx context.Context, inst core.Instance) (core.IngressHandle, error) {
	bound, ok := kindOwners().For(inst.Kind)
	if !ok || bound.Ingress == nil {
		return core.IngressHandle{}, nil
	}
	return bound.Ingress.IngressHandle(ctx, inst)
}

/*
ExitHandleFor resolves one uplink's handle, and what kind of handle it is.

Both together because a caller needs the kind to know how to route to it and the
handle to know where: asking twice would let the two disagree while a device
came up between the calls.
*/
func ExitHandleFor(ctx context.Context, exit core.Exit) (core.ExitHandleKind, core.ExitHandle, error) {
	bound, ok := kindOwners().For(exit.Kind)
	if !ok || bound.Egress == nil {
		return core.ExitNone, core.ExitHandle{}, nil
	}
	kind := bound.Egress.ExitHandleKind(exit.Kind)
	if kind == core.ExitNone {
		return core.ExitNone, core.ExitHandle{}, nil
	}
	handle, err := bound.Egress.ExitHandle(ctx, exit)
	if err != nil {
		return core.ExitNone, core.ExitHandle{}, err
	}
	return kind, handle, nil
}

// ExitKinds names every kind that can terminate a route in this build, so a
// picker is built from the registry rather than from a list somebody maintains.
func ExitKinds() []core.Kind {
	var out []core.Kind
	for _, kind := range Kinds() {
		bound, ok := kindOwners().For(kind)
		if !ok || bound.Egress == nil {
			continue
		}
		if bound.Egress.ExitHandleKind(kind) != core.ExitNone {
			out = append(out, kind)
		}
	}
	return out
}
