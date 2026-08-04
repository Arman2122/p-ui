// Package mtproto adapts the mtg-multi engine to the core contract. It is the
// first core ported, so the contract was shaped to fit a real daemon.
package mtproto

import (
	"context"
	"slices"
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

	mu     sync.Mutex
	online []string
}

// New returns a core over the process-wide mtg manager.
func New() *Core { return &Core{mgr: engine.GetManager()} }

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
