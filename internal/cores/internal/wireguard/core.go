// Package wireguard adapts kernel WireGuard to the core contract. It is the
// first core the panel converges through netlink instead of a daemon.
package wireguard

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"sync"

	"github.com/Arman2122/p-ui/internal/core"
	engine "github.com/Arman2122/p-ui/internal/wireguard"
)

// Kind is the protocol this core serves. Xray's own "wireguard" kind is a
// different thing — a userspace tunnel it proxies — and keeps that name.
const Kind core.Kind = "wgkernel"

// Core is the kernel WireGuard adapter. The engine owns the devices; the only
// state here is the last scrape's online set, which OnlineEmails replays.
type Core struct {
	mgr *engine.Manager

	mu          sync.Mutex
	online      []string
	pendingTags map[string]core.TagDelta
}

// New returns a core over the process-wide device manager.
func New() *Core { return &Core{mgr: engine.GetManager()} }

func (c *Core) Kinds() []core.Kind { return []core.Kind{Kind} }

func (c *Core) Describe() core.Descriptor {
	return core.Descriptor{
		ID:       Kind,
		TitleKey: "cores.wgkernel.title",
		Caps: core.Capabilities{
			UserHotAdd:   core.Yes(),
			PerUserStats: core.Yes(),
			OnlineUsers:  core.Yes(),
			// The kernel holds no per-peer byte budget, and the .conf a client
			// needs is still built by the panel's own link builders.
			QuotaPushdown: core.No(),
			ShareLink:     core.No(),
		},
	}
}

// Preflight fails on a host without kernel WireGuard, which disables this core
// alone rather than failing one inbound at a time.
func (c *Core) Preflight(ctx context.Context) error { return c.mgr.Preflight(ctx) }

// ClientCredentials names what a client of this core carries. keepAlive is not
// among them: it is outside the vocabulary a client form can render.
func (c *Core) ClientCredentials(kind core.Kind) []string {
	if kind != Kind {
		return nil
	}
	return []string{core.CredPrivateKey, core.CredPublicKey, core.CredPreSharedKey, core.CredAllowedIPs}
}

func (c *Core) Reconcile(ctx context.Context, desired []core.Instance) error {
	want := make([]engine.Instance, 0, len(desired))
	var keep []int
	var failures []error
	for _, d := range desired {
		inst, serve, err := toEngine(d)
		switch {
		case err != nil:
			// Omitting it would read as "no longer desired" and delete the device
			// with every peer on it, for settings that merely will not parse.
			keep = append(keep, d.ID)
			failures = append(failures, fmt.Errorf("wgkernel: inbound %d: %w", d.ID, err))
		case serve:
			want = append(want, inst)
		}
	}
	failures = append(failures, c.mgr.Reconcile(ctx, want, keep...))
	return errors.Join(failures...)
}

// StopAll leaves every device up. A kernel interface outlives the panel process,
// so an upgrade must not disconnect everyone the way stopping a sidecar does.
func (c *Core) StopAll(ctx context.Context) error { return c.mgr.StopAll(ctx) }

// PlanChange mirrors what Ensure does. A rename has to be a no-op: the device is
// keyed by inbound id, and anything else has UpdateInbound delete it first.
func (c *Core) PlanChange(before, after core.Instance) core.Action {
	b, bServe, bErr := toEngine(before)
	a, aServe, aErr := toEngine(after)
	switch {
	case bErr != nil || aErr != nil:
		// Settings that will not parse cannot be proven unchanged, and applying
		// the edit is where the operator is told why.
		return core.ActionHotApply
	case bServe != aServe:
		// The device is created or deleted, which ends every client's session.
		return core.ActionRestart
	case b.DeviceFingerprint() != a.DeviceFingerprint(), b.PeersFingerprint() != a.PeersFingerprint():
		return core.ActionHotApply
	default:
		return core.ActionNoop
	}
}

// ApplyInstance converges one inbound's device. Only an inbound that must serve
// nobody has its device removed; unreadable settings leave it exactly as it is.
func (c *Core) ApplyInstance(ctx context.Context, inst core.Instance) error {
	want, serve, err := toEngine(inst)
	if err != nil {
		return fmt.Errorf("wgkernel: inbound %d: %w", inst.ID, err)
	}
	if !serve {
		return c.mgr.Remove(ctx, inst.ID)
	}
	return c.mgr.Ensure(ctx, want)
}

func (c *Core) DropInstance(ctx context.Context, inst core.Instance) error {
	return c.mgr.Remove(ctx, inst.ID)
}

// AddUser upserts one client's peer, reading no other client. A kernel peer is
// authenticated by key alone, so a client without one cannot be added.
func (c *Core) AddUser(ctx context.Context, inst core.Instance, user core.User) error {
	want, serve, err := toEngine(inst)
	if err != nil {
		return fmt.Errorf("wgkernel: inbound %d: %w", inst.ID, err)
	}
	if !serve {
		// An inbound that serves nobody has no device to add to, and the panel
		// dispatches this for a client added to a disabled one.
		return nil
	}
	// The same filter toEngine applies to the set: a disabled client the kernel
	// holds a key for is a client the panel believes it revoked.
	if !user.Enable {
		return nil
	}
	peer, ok := toPeer(user)
	if !ok {
		return fmt.Errorf("wgkernel: client %q carries no public key", user.Email)
	}
	return c.mgr.AddPeer(ctx, want, peer)
}

// RemoveUser is handed an instance with no user set, so the public key its peer
// is held under comes from the engine's index or, cold, the stored settings.
func (c *Core) RemoveUser(ctx context.Context, inst core.Instance, email string) error {
	// A revocation must land whatever else is wrong with the inbound, so neither a
	// disabled one nor unreadable settings stop it: it needs only id and address.
	want, _, _ := toEngine(inst)
	return c.mgr.RemovePeer(ctx, want, email, engine.PeerKeyFromSettings(inst.Settings, email))
}

func (c *Core) CollectTraffic(ctx context.Context) ([]core.TrafficDelta, error) {
	billed, online := c.mgr.CollectTraffic(ctx)
	c.mu.Lock()
	c.online = online
	c.stashTagTraffic(billed)
	c.mu.Unlock()

	out := make([]core.TrafficDelta, 0, len(billed))
	for _, t := range billed {
		out = append(out, core.TrafficDelta{Email: t.Email, Tag: t.Tag, Up: t.Up, Down: t.Down})
	}
	return out, nil
}

// stashTagTraffic banks each inbound's total, rolled up from the clients on it.
// The kernel meters per peer only, so the total is exactly that sum.
func (c *Core) stashTagTraffic(billed []engine.Traffic) {
	for _, t := range billed {
		if t.Tag == "" {
			continue
		}
		if c.pendingTags == nil {
			c.pendingTags = make(map[string]core.TagDelta, len(billed))
		}
		acc := c.pendingTags[t.Tag]
		acc.Tag = t.Tag
		acc.Up += t.Up
		acc.Down += t.Down
		c.pendingTags[t.Tag] = acc
	}
}

// CollectTagTraffic drains what CollectTraffic banked. Draining, not replaying:
// handing the same delta out twice doubles the inbound's total.
func (c *Core) CollectTagTraffic(_ context.Context) ([]core.TagDelta, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.pendingTags) == 0 {
		return nil, nil
	}
	out := make([]core.TagDelta, 0, len(c.pendingTags))
	for _, d := range c.pendingTags {
		out = append(out, d)
	}
	c.pendingTags = nil
	slices.SortFunc(out, func(a, b core.TagDelta) int { return strings.Compare(a.Tag, b.Tag) })
	return out, nil
}

// OnlineEmails replays the last traffic scrape. Scraping again would advance the
// byte counters and discard that delta — online status is worth less than bytes.
func (c *Core) OnlineEmails(_ context.Context) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.online), nil
}

// RemovalLosesCounters is measured, not inferred: a peer remove ZEROES rx/tx
// while an allowed-IP, keepalive or preshared-key edit preserves them.
func (c *Core) RemovalLosesCounters(kind core.Kind) bool { return kind == Kind }

// ShapingSelector answers for this core's own kind alone. A peer's allowed-IP is
// unforgeable: cryptokey routing drops a spoofed inner source at decap.
func (c *Core) ShapingSelector(kind core.Kind) core.Selector {
	if kind != Kind {
		return core.SelectorNone
	}
	return core.SelectorInnerIP
}

/*
ShapingTargets reads each client's identity off the device, never out of the
panel's own settings: the kernel moves a shared allowed-IP to the later peer, so
it is the only thing that knows who currently answers to an address.

A client with no single-address prefix is left out rather than keyed by a guess —
a wider prefix would shape everyone inside it as that one client.
*/
func (c *Core) ShapingTargets(ctx context.Context, inst core.Instance) (core.ShapingTarget, error) {
	held, err := c.mgr.PeerAllowedIPs(ctx, inst.ID)
	if err != nil {
		if errors.Is(err, engine.ErrNoDevice) {
			// Not hosting it at this moment; the caller goes quiet and retries.
			return core.ShapingTarget{}, nil
		}
		return core.ShapingTarget{}, fmt.Errorf("wgkernel: inbound %d: %w", inst.ID, err)
	}
	target := core.ShapingTarget{Device: engine.InterfaceName(inst.ID), Selector: core.SelectorInnerIP}
	for _, u := range inst.Users {
		hosts := hostPrefixes(held[u.Email])
		if len(hosts) == 0 {
			continue
		}
		if target.Keys == nil {
			target.Keys = make(map[string]core.SubjectKey, len(inst.Users))
		}
		target.Keys[u.Email] = core.SubjectKey{Prefixes: hosts}
	}
	return target, nil
}

// hostPrefixes keeps the prefixes holding a single address. A site-to-site peer's
// /24 is a real configuration, and it is not an identity anything may be keyed on.
func hostPrefixes(held []netip.Prefix) []netip.Prefix {
	out := make([]netip.Prefix, 0, len(held))
	for _, prefix := range held {
		if prefix.IsSingleIP() {
			out = append(out, prefix)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Sessions names each live tunnel and the outer address it handshook from: the
// address that roams when two devices share one key, which is the sharing signal.
func (c *Core) Sessions(ctx context.Context) ([]core.Session, error) {
	live := c.mgr.Endpoints(ctx)
	out := make([]core.Session, 0, len(live))
	for _, e := range live {
		out = append(out, core.Session{
			Email:             e.Email,
			Source:            e.Source,
			Local:             e.Local,
			LastSeenUnixMilli: e.Handshake.UnixMilli(),
		})
	}
	return out, nil
}
