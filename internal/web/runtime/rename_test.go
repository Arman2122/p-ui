package runtime

import (
	"context"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
)

// planCore is a core whose only interesting behaviour is how it classifies a
// rename, which is the fact UpdateInbound has to ask about.
type planCore struct {
	kind     core.Kind
	onRename core.Action
	dropped  []string
	applied  []string
}

func (c *planCore) Describe() core.Descriptor {
	return core.Descriptor{ID: c.kind, TitleKey: "core.plan"}
}
func (c *planCore) Kinds() []core.Kind              { return []core.Kind{c.kind} }
func (c *planCore) Preflight(context.Context) error { return nil }

// Modelled on mtg: a changed user set reloads in place, and the tag is the only
// thing whose treatment varies by core. Without the user arm the fake answers
// the same either way and cannot show the tag being isolated.
func (c *planCore) PlanChange(before, after core.Instance) core.Action {
	if len(before.Users) != len(after.Users) {
		return core.ActionHotApply
	}
	if before.Tag != after.Tag {
		return c.onRename
	}
	return core.ActionNoop
}

func (c *planCore) ApplyInstance(_ context.Context, inst core.Instance) error {
	c.applied = append(c.applied, inst.Tag)
	return nil
}

func (c *planCore) DropInstance(_ context.Context, inst core.Instance) error {
	c.dropped = append(c.dropped, inst.Tag)
	return nil
}

/*
Renaming an inbound must not drop it on a core that does not key by tag.

Dropping an mtproto inbound stops its mtg sidecar, so treating every rename as a
move killed every live Telegram connection on it — for an edit the engine cannot
even see, since the tag is in neither of its fingerprints. Xray is the opposite:
its config is keyed by tag, so skipping the drop strands the old listener on the
port. One rule cannot serve both, which is why the core is asked.
*/
func TestRenameDropsOnlyWhereTheTagIsTheIdentity(t *testing.T) {
	for _, tc := range []struct {
		name      string
		onRename  core.Action
		wantDrops []string
	}{
		{
			name:      "a core that reports a rename as a no-op keeps its daemon",
			onRename:  core.ActionNoop,
			wantDrops: nil,
		},
		{
			name:      "a core that hot-applies a rename is keyed by tag, so the old tag must go",
			onRename:  core.ActionHotApply,
			wantDrops: []string{"before"},
		},
		{
			name:      "a core that restarts on a rename is also keyed by tag",
			onRename:  core.ActionRestart,
			wantDrops: []string{"before"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := &planCore{kind: "planned", onRename: tc.onRename}
			registry := core.NewRegistry()
			if err := registry.Register(c); err != nil {
				t.Fatalf("register: %v", err)
			}
			l := NewLocal(LocalDeps{Cores: registry})

			oldIb := &model.Inbound{Id: 1, Protocol: "planned", Tag: "before", Port: 443, Enable: true}
			newIb := &model.Inbound{Id: 1, Protocol: "planned", Tag: "after", Port: 443, Enable: true}
			if err := l.UpdateInbound(t.Context(), oldIb, newIb); err != nil {
				t.Fatalf("UpdateInbound: %v", err)
			}

			if len(c.dropped) != len(tc.wantDrops) {
				t.Fatalf("dropped %v, want %v", c.dropped, tc.wantDrops)
			}
			for i, tag := range tc.wantDrops {
				if c.dropped[i] != tag {
					t.Errorf("dropped[%d] = %q, want %q", i, c.dropped[i], tag)
				}
			}
			if len(c.applied) != 1 || c.applied[0] != "after" {
				t.Errorf("applied %v, want the renamed inbound applied exactly once", c.applied)
			}
		})
	}
}

/*
The rename question must be asked about the rename alone.

Judging the whole edit would put a rename bundled with a client change back on
the restart path for a core that reloads secrets in place — the same dropped
connections by a longer route.
*/
func TestRenameIsJudgedIndependentlyOfTheOtherEdits(t *testing.T) {
	c := &planCore{kind: "planned", onRename: core.ActionNoop}
	registry := core.NewRegistry()
	if err := registry.Register(c); err != nil {
		t.Fatalf("register: %v", err)
	}
	l := NewLocal(LocalDeps{Cores: registry})

	oldIb := &model.Inbound{
		Id: 1, Protocol: "planned", Tag: "before", Port: 443, Enable: true,
		Settings: `{"clients":[{"email":"a@x","enable":true}]}`,
	}
	newIb := &model.Inbound{
		Id: 1, Protocol: "planned", Tag: "after", Port: 443, Enable: true,
		Settings: `{"clients":[{"email":"a@x","enable":true},{"email":"b@x","enable":true}]}`,
	}
	if err := l.UpdateInbound(t.Context(), oldIb, newIb); err != nil {
		t.Fatalf("UpdateInbound: %v", err)
	}
	if len(c.dropped) != 0 {
		t.Errorf("dropped %v; adding a client alongside the rename must not turn it into a move", c.dropped)
	}
}

// A protocol change hands the inbound to a different core, which cannot know to
// clean up after the old one, so that drop is unconditional.
func TestAProtocolChangeAlwaysDropsTheOldInbound(t *testing.T) {
	oldCore := &planCore{kind: "planned", onRename: core.ActionNoop}
	newCore := &planCore{kind: "replanned", onRename: core.ActionNoop}
	registry := core.NewRegistry()
	for _, c := range []*planCore{oldCore, newCore} {
		if err := registry.Register(c); err != nil {
			t.Fatalf("register %s: %v", c.kind, err)
		}
	}
	l := NewLocal(LocalDeps{Cores: registry})

	oldIb := &model.Inbound{Id: 1, Protocol: "planned", Tag: "same", Port: 443, Enable: true}
	newIb := &model.Inbound{Id: 1, Protocol: "replanned", Tag: "same", Port: 443, Enable: true}
	if err := l.UpdateInbound(t.Context(), oldIb, newIb); err != nil {
		t.Fatalf("UpdateInbound: %v", err)
	}
	if len(oldCore.dropped) != 1 {
		t.Errorf("the old core dropped %v, want the inbound dropped once", oldCore.dropped)
	}
	if len(newCore.applied) != 1 {
		t.Errorf("the new core applied %v, want the inbound applied once", newCore.applied)
	}
}
