package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
)

type LocalDeps struct {
	SetNeedRestart func()
	// Cores resolves an inbound's protocol to the core that serves it. Nil in
	// tests that never touch an inbound; every inbound op fails loudly without it.
	Cores *core.Registry
	/*
		LoadInbound re-reads the row a user op names, for the cores that need it.

		A core that provisions users by re-applying its whole set rebuilds that
		set from Instance.Users, so it revokes every client missing from the row
		it is handed — and callers hand Local the copy they were editing. Nil
		leaves that copy standing, which is what a runtime test wires; the
		production wiring is pinned by internal/arch.
	*/
	LoadInbound func(id int) (*model.Inbound, error)
	/*
		RenderInbound produces the inbound exactly as the full config build would
		emit it, so a hot apply and the next restart agree byte for byte.

		A nil config means the local Xray does not serve this inbound and the
		stored sections stand — that is how an mtproto inbound reaches its own
		core untouched. Nil the field itself and every inbound falls back to
		those stored sections, which is the pre-unification behaviour.
	*/
	RenderInbound func(ib *model.Inbound) (*core.InboundConfig, error)
}

type Local struct {
	deps LocalDeps
}

func NewLocal(deps LocalDeps) *Local {
	return &Local{deps: deps}
}

func (l *Local) Name() string { return "local" }

// coreFor resolves the core serving this inbound. An unknown protocol is an
// error, never a no-op: silence would read as "applied" and quarantine as delete.
func (l *Local) coreFor(ib *model.Inbound) (*core.Bound, error) {
	if l.deps.Cores == nil {
		return nil, errors.New("local runtime has no core registry")
	}
	bound, ok := l.deps.Cores.For(core.Kind(ib.Protocol))
	if !ok {
		return nil, fmt.Errorf("no core serves protocol %q", ib.Protocol)
	}
	return bound, nil
}

func (l *Local) applierFor(ib *model.Inbound) (*core.Bound, error) {
	bound, err := l.coreFor(ib)
	if err != nil {
		return nil, err
	}
	if bound.Apply == nil {
		return nil, fmt.Errorf("core %q cannot apply a single inbound", bound.Core.Describe().ID)
	}
	return bound, nil
}

/*
desiredState renders the inbound the way a restart would.

Without this the hot path applied the stored sections while a restart applied
the generated ones, so an inbound edited under load kept quota-exhausted
clients, lost its fallbacks, and carried panel-only fields Xray never should
see. Worse, InboundConfig.Equals compares bytes, so the running inbound then
stopped matching the generator and every restart check read a pending change.
*/
func (l *Local) desiredState(ib *model.Inbound) (core.Instance, error) {
	inst := instanceOf(ib)
	if l.deps.RenderInbound == nil {
		return inst, nil
	}
	rendered, err := l.deps.RenderInbound(ib)
	if err != nil {
		return inst, fmt.Errorf("render inbound %q: %w", ib.Tag, err)
	}
	if rendered == nil {
		return inst, nil
	}
	inst.Settings = string(rendered.Settings)
	inst.StreamSettings = string(rendered.StreamSettings)
	inst.Sniffing = string(rendered.Sniffing)
	return inst, nil
}

func (l *Local) AddInbound(ctx context.Context, ib *model.Inbound) error {
	bound, err := l.applierFor(ib)
	if err != nil {
		return err
	}
	inst, err := l.desiredState(ib)
	if err != nil {
		return err
	}
	return bound.Apply.ApplyInstance(ctx, inst)
}

// DropInstance is keyed by tag and port, so the stored sections are enough and
// a render failure must not be able to strand a listener.
func (l *Local) DelInbound(ctx context.Context, ib *model.Inbound) error {
	bound, err := l.applierFor(ib)
	if err != nil {
		return err
	}
	return bound.Apply.DropInstance(ctx, instanceOf(ib))
}

/*
UpdateInbound converges the new state in place rather than removing and re-adding.
ApplyInstance is what lets a core keep the connections it can: mtg reloads its
secrets without restarting, and Xray diffs the inbound down to the users that
actually changed.

The old inbound is dropped first only when applying the new one would strand it.
The drop is not fatal here — the old core may legitimately no longer be serving it.
*/
func (l *Local) UpdateInbound(ctx context.Context, oldIb, newIb *model.Inbound) error {
	if l.strandsOldInbound(oldIb, newIb) {
		_ = l.DelInbound(ctx, oldIb)
	}
	if !newIb.Enable {
		return l.DelInbound(ctx, newIb)
	}
	return l.AddInbound(ctx, newIb)
}

/*
strandsOldInbound reports whether applying newIb would leave oldIb behind.

A changed protocol hands the inbound to a different core, which cannot know to
clean up after the old one. A changed tag only strands it in a core that keys by
tag: Xray's config does, so the old tag would keep its listener, but mtg is keyed
by inbound id and a rename changes nothing it can see.

Asking the core beats assuming, because the assumption was wrong and expensive —
dropping an mtproto inbound stops the sidecar, so renaming one used to kill every
live Telegram connection on it. The question isolates the tag deliberately: a
rename bundled with a client edit must still be judged on the rename alone.
*/
func (l *Local) strandsOldInbound(oldIb, newIb *model.Inbound) bool {
	if oldIb.Protocol != newIb.Protocol {
		return true
	}
	if oldIb.Tag == newIb.Tag {
		return false
	}
	bound, err := l.coreFor(oldIb)
	if err != nil || bound.HotApply == nil {
		return true
	}
	before := instanceOf(oldIb)
	renamed := before
	renamed.Tag = newIb.Tag
	return bound.HotApply.PlanChange(before, renamed) != core.ActionNoop
}

/*
userTarget resolves the core and the state a user op acts on. named is the one
client the op provisions, "" when it only has to name one.

A whole-set core reads every other client out of the instance, so it is rebuilt
from the current row rather than the caller's copy: the copy is as old as the
caller's own read, and on such a core "absent" means "revoked". Every other core
touches the named client alone, and re-reading the row and re-projecting its
clients for it cost O(inbound size) — 1.3s on a 200k-client inbound — per edit,
which callers pay once per client in a fan-out.
*/
func (l *Local) userTarget(ib *model.Inbound, named string) (*core.Bound, core.Instance, error) {
	bound, err := l.coreFor(ib)
	if err != nil {
		return nil, core.Instance{}, err
	}
	if bound.Users == nil {
		return nil, core.Instance{}, fmt.Errorf("core %q cannot provision a single user", bound.Core.Describe().ID)
	}
	if bound.UserSet == nil {
		inst := storedInstanceOf(ib)
		if named != "" {
			inst.Users = usersOf(ib.Settings, named)
		}
		return bound, inst, nil
	}
	current := ib
	if l.deps.LoadInbound != nil {
		fresh, loadErr := l.deps.LoadInbound(ib.Id)
		if loadErr != nil {
			return nil, core.Instance{}, fmt.Errorf("reload inbound %d: %w", ib.Id, loadErr)
		}
		if fresh != nil {
			current = fresh
		}
	}
	return bound, instanceOf(current), nil
}

// AddUser hands the core the client as the inbound stores it. The credentials a
// protocol needs are the core's business, so nothing here names one.
func (l *Local) AddUser(ctx context.Context, ib *model.Inbound, email string) error {
	bound, inst, err := l.userTarget(ib, email)
	if err != nil {
		return err
	}
	for _, user := range inst.Users {
		if user.Email == email {
			return bound.Users.AddUser(ctx, inst, user)
		}
	}
	return fmt.Errorf("inbound %q carries no client %q to add", inst.Tag, email)
}

// RemoveUser names the client; it never has to describe one, so no core but a
// whole-set one is handed a projection of the inbound's clients here.
func (l *Local) RemoveUser(ctx context.Context, ib *model.Inbound, email string) error {
	bound, inst, err := l.userTarget(ib, "")
	if err != nil {
		return err
	}
	return bound.Users.RemoveUser(ctx, inst, email)
}

func (l *Local) AddClient(ctx context.Context, ib *model.Inbound, client model.Client) error {
	if !client.Enable {
		return nil
	}
	return l.AddUser(ctx, ib, client.Email)
}

func (l *Local) DeleteUser(ctx context.Context, ib *model.Inbound, email string) error {
	if email == "" {
		return nil
	}
	if err := l.RemoveUser(ctx, ib, email); err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil
		}
		return err
	}
	return nil
}

func (l *Local) DeleteClient(context.Context, string) error {
	return nil
}

func (l *Local) UpdateUser(ctx context.Context, ib *model.Inbound, oldEmail string, payload model.Client) error {
	if oldEmail != "" {
		if err := l.RemoveUser(ctx, ib, oldEmail); err != nil && !strings.Contains(err.Error(), "not found") {
			return err
		}
	}
	if !payload.Enable {
		return nil
	}
	return l.AddUser(ctx, ib, payload.Email)
}

func (l *Local) RestartXray(_ context.Context) error {
	if l.deps.SetNeedRestart != nil {
		l.deps.SetNeedRestart()
	}
	return nil
}

func (l *Local) ResetClientTraffic(_ context.Context, _ *model.Inbound, _ string) error {
	return nil
}

func (l *Local) ResetAllTraffics(_ context.Context) error {
	return nil
}

func (l *Local) ResetInboundTraffic(_ context.Context, _ *model.Inbound) error {
	return nil
}
