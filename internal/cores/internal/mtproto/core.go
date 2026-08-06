// Package mtproto adapts the mtg-multi engine to the core contract. It is the
// first core ported, so the contract was shaped to fit a real daemon.
package mtproto

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/Arman2122/p-ui/internal/core"
	engine "github.com/Arman2122/p-ui/internal/mtproto"
)

// Kind is the protocol this core serves. It is persisted in inbounds.protocol,
// so it is never renamed.
const Kind core.Kind = "mtproto"

// Core is the mtproto adapter. It owns no state of its own beyond the last
// online snapshot; the engine holds the processes.
type Core struct {
	mgr *engine.Manager

	// routedTags answers which running tags egress through Xray. A field so a
	// test can state it; in production it is always the engine's own answer.
	routedTags func() map[string]bool

	mu          sync.Mutex
	online      []string
	pendingTags map[string]core.TagDelta
}

// New returns a core over the process-wide mtg manager.
func New() *Core {
	mgr := engine.GetManager()
	return &Core{mgr: mgr, routedTags: mgr.RoutedTags}
}

func (c *Core) Kinds() []core.Kind { return []core.Kind{Kind} }

func (c *Core) Describe() core.Descriptor {
	return core.Descriptor{
		ID:       Kind,
		TitleKey: "cores.mtproto.title",
		Caps: core.Capabilities{
			UserHotAdd:    core.Yes(),
			PerUserStats:  core.Yes(),
			QuotaPushdown: core.Yes(),
			OnlineUsers:   core.Yes(),
			// Share links are still rendered by the panel's own link builders;
			// consolidating them onto LinkRenderer is a later phase.
			ShareLink: core.No(),
		},
	}
}

// Preflight fails when the mtg binary is missing, which disables the core
// instead of letting every reconcile fail one inbound at a time.
func (c *Core) Preflight(_ context.Context) error {
	return engine.CheckBinary()
}

// ClientCredentials names what a client of this core needs: a FakeTLS secret,
// and the optional ad tag mtg bills promoted channels against.
func (c *Core) ClientCredentials(kind core.Kind) []string {
	if kind != Kind {
		return nil
	}
	return []string{core.CredSecret, core.CredAdTag}
}

func (c *Core) Reconcile(_ context.Context, desired []core.Instance) error {
	want := make([]engine.Instance, 0, len(desired))
	for _, d := range desired {
		if inst, ok := toEngine(d); ok {
			want = append(want, inst)
		}
	}
	return c.mgr.Reconcile(want)
}

func (c *Core) StopAll(_ context.Context) error {
	c.mgr.StopAll()
	return nil
}

// PlanChange mirrors what the engine will actually do: anything outside the
// [secrets] section needs a restart, a secrets-only edit is applied in place.
func (c *Core) PlanChange(before, after core.Instance) core.Action {
	b, bOK := toEngine(before)
	a, aOK := toEngine(after)
	switch {
	case bOK != aOK:
		// One side has nothing to serve — disabled, or its last keyed client
		// gone. The sidecar is started or stopped, never reloaded in place.
		return core.ActionRestart
	case b.StructuralFingerprint() != a.StructuralFingerprint():
		return core.ActionRestart
	case b.SecretsFingerprint() != a.SecretsFingerprint():
		return core.ActionHotApply
	default:
		return core.ActionNoop
	}
}

// ApplyInstance pushes one sidecar's desired state. The engine keeps the
// process and its live connections whenever only the secrets changed.
func (c *Core) ApplyInstance(_ context.Context, inst core.Instance) error {
	return c.apply(inst)
}

func (c *Core) DropInstance(_ context.Context, inst core.Instance) error {
	c.mgr.Remove(inst.ID)
	return nil
}

// ProvisionsWholeUserSet: both user ops push the whole [secrets] section, so a
// client missing from the instance they are handed is a client revoked.
func (c *Core) ProvisionsWholeUserSet() {}

// AddUser is an upsert: re-adding an existing email replaces its credentials,
// so a re-key and an add take the same path.
func (c *Core) AddUser(_ context.Context, inst core.Instance, user core.User) error {
	next := withoutUser(inst, user.Email)
	next.Users = append(next.Users, user)
	return c.apply(next)
}

func (c *Core) RemoveUser(_ context.Context, inst core.Instance, email string) error {
	return c.apply(withoutUser(inst, email))
}

// apply pushes one instance's current user set. An instance with no serveable
// user is stopped, not started with an empty [secrets] section mtg would reject.
func (c *Core) apply(inst core.Instance) error {
	want, ok := toEngine(inst)
	if !ok {
		c.mgr.Remove(inst.ID)
		return nil
	}
	return c.mgr.Ensure(want)
}

func withoutUser(inst core.Instance, email string) core.Instance {
	next := inst
	next.Users = slices.DeleteFunc(slices.Clone(inst.Users), func(u core.User) bool {
		return u.Email == email
	})
	return next
}

func (c *Core) CollectTraffic(_ context.Context) ([]core.TrafficDelta, error) {
	billed, online := c.mgr.CollectTraffic()
	c.mu.Lock()
	c.online = online
	c.mu.Unlock()

	out := make([]core.TrafficDelta, 0, len(billed))
	for _, t := range billed {
		out = append(out, core.TrafficDelta{Email: t.Email, Tag: t.Tag, Up: t.Up, Down: t.Down})
	}
	c.stashTagTraffic(billed)
	return out, nil
}

/*
stashTagTraffic banks each inbound's total, rolled up from the clients on it.

mtg meters per secret and nothing else, so an inbound's total is exactly the sum
of its clients'. A tag that egresses through Xray is skipped: the bridge already
counted those bytes, and billing them here too would double them.
*/
func (c *Core) stashTagTraffic(billed []engine.Traffic) {
	// Asked of the engine every scrape, not remembered here: nothing calls this
	// core's Reconcile, so a map filled on apply would still be empty after a
	// panel restart — and an empty one bills every routed inbound twice.
	var routed map[string]bool
	if c.routedTags != nil {
		routed = c.routedTags()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, t := range billed {
		if t.Tag == "" || routed[t.Tag] {
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
// see the xray core for why handing the same delta out twice doubles a total.
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

func (c *Core) ResetQuota(_ context.Context, email string) error {
	return c.mgr.ResetQuota(email)
}
