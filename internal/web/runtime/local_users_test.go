package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
)

// setCore is modelled on mtg: it provisions by re-applying the whole set it is
// handed, so whatever is missing from that set is revoked.
type setCore struct {
	kind      core.Kind
	sawUsers  []core.User
	sawUser   core.User
	sawEmail  string
	addCalls  int
	dropCalls int
}

func (c *setCore) Describe() core.Descriptor {
	return core.Descriptor{ID: c.kind, TitleKey: "core.set"}
}
func (c *setCore) Kinds() []core.Kind              { return []core.Kind{c.kind} }
func (c *setCore) Preflight(context.Context) error { return nil }

func (c *setCore) AddUser(_ context.Context, inst core.Instance, user core.User) error {
	c.addCalls++
	c.sawUsers, c.sawUser = inst.Users, user
	return nil
}

func (c *setCore) RemoveUser(_ context.Context, inst core.Instance, email string) error {
	c.dropCalls++
	c.sawUsers, c.sawEmail = inst.Users, email
	return nil
}

// mutedCore serves the same kind but provisions no users, which is how a core
// that only reconciles whole instances looks to the registry.
type mutedCore struct{ kind core.Kind }

func (c *mutedCore) Describe() core.Descriptor {
	return core.Descriptor{ID: c.kind, TitleKey: "core.muted"}
}
func (c *mutedCore) Kinds() []core.Kind              { return []core.Kind{c.kind} }
func (c *mutedCore) Preflight(context.Context) error { return nil }

func emailsOf(users []core.User) []string {
	out := make([]string, 0, len(users))
	for _, u := range users {
		out = append(out, u.Email)
	}
	return out
}

func localFor(t *testing.T, c core.Core, current *model.Inbound) *Local {
	t.Helper()
	registry := core.NewRegistry()
	if err := registry.Register(c); err != nil {
		t.Fatalf("register: %v", err)
	}
	return NewLocal(LocalDeps{
		Cores:       registry,
		LoadInbound: func(int) (*model.Inbound, error) { return current, nil },
	})
}

/*
A user op is applied against the inbound as it stands, not the caller's copy.

The copy is at least as old as the caller's own read, and on a core that
re-applies its whole user set every client absent from it is a client revoked —
which is why routing these two calls was deferred until the row was refreshed.
*/
func TestAUserOpAppliesTheInboundAsItIsNow(t *testing.T) {
	// The caller edited "a" and never saw "b"; the row now carries both.
	stale := &model.Inbound{
		Id: 7, Protocol: "setcore", Tag: "in-7", Port: 443, Enable: true,
		Settings: `{"clients":[{"email":"a@x","secret":"ee01","enable":true}]}`,
	}
	current := &model.Inbound{
		Id: 7, Protocol: "setcore", Tag: "in-7", Port: 443, Enable: true,
		Settings: `{"clients":[{"email":"a@x","secret":"ee01","enable":true},{"email":"b@x","secret":"ee02","enable":true}]}`,
	}

	t.Run("add", func(t *testing.T) {
		c := &setCore{kind: "setcore"}
		if err := localFor(t, c, current).AddUser(t.Context(), stale, "b@x"); err != nil {
			t.Fatalf("AddUser: %v", err)
		}
		if c.addCalls != 1 {
			t.Fatalf("the core was asked to add %d time(s), want exactly 1", c.addCalls)
		}
		if got, want := strings.Join(emailsOf(c.sawUsers), ","), "a@x,b@x"; got != want {
			t.Errorf("the core was handed the set %q, want %q — the clients it cannot see are the ones it revokes", got, want)
		}
		if c.sawUser.Email != "b@x" {
			t.Errorf("the core was told to add %q, want %q", c.sawUser.Email, "b@x")
		}
		if got := c.sawUser.Credentials["secret"]; got != "ee02" {
			t.Errorf("added user carries secret %v, want %q — credentials must come from the inbound, not from the caller", got, "ee02")
		}
	})

	t.Run("remove", func(t *testing.T) {
		c := &setCore{kind: "setcore"}
		if err := localFor(t, c, current).RemoveUser(t.Context(), stale, "a@x"); err != nil {
			t.Fatalf("RemoveUser: %v", err)
		}
		if c.dropCalls != 1 {
			t.Fatalf("the core was asked to remove %d time(s), want exactly 1", c.dropCalls)
		}
		if got, want := strings.Join(emailsOf(c.sawUsers), ","), "a@x,b@x"; got != want {
			t.Errorf("the core was handed the set %q, want %q", got, want)
		}
		if c.sawEmail != "a@x" {
			t.Errorf("the core was told to remove %q, want %q", c.sawEmail, "a@x")
		}
	})
}

// A user op that cannot reach a core must say so: reporting success leaves the
// panel showing a client the daemon never learned about, or still serving a revoked one.
func TestAUserOpThatCannotBeAppliedFailsLoudly(t *testing.T) {
	ib := &model.Inbound{
		Id: 7, Protocol: "setcore", Tag: "in-7", Port: 443, Enable: true,
		Settings: `{"clients":[{"email":"a@x","secret":"ee01","enable":true}]}`,
	}

	for _, tc := range []struct {
		name string
		run  func(t *testing.T) error
		want string
	}{
		{
			name: "the core provisions no users",
			run: func(t *testing.T) error {
				return localFor(t, &mutedCore{kind: "setcore"}, ib).AddUser(t.Context(), ib, "a@x")
			},
			want: "cannot provision",
		},
		{
			name: "the inbound does not carry the client",
			run: func(t *testing.T) error {
				return localFor(t, &setCore{kind: "setcore"}, ib).AddUser(t.Context(), ib, "ghost@x")
			},
			want: "ghost@x",
		},
		{
			name: "the row cannot be re-read",
			run: func(t *testing.T) error {
				registry := core.NewRegistry()
				if err := registry.Register(&setCore{kind: "setcore"}); err != nil {
					t.Fatalf("register: %v", err)
				}
				l := NewLocal(LocalDeps{Cores: registry, LoadInbound: func(int) (*model.Inbound, error) {
					return nil, context.DeadlineExceeded
				}})
				return l.RemoveUser(t.Context(), ib, "a@x")
			},
			want: "reload inbound 7",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run(t)
			if err == nil {
				t.Fatal("a user op that did not reach a core must report an error, not success")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error must say why; got %v, want it to mention %q", err, tc.want)
			}
		})
	}
}
