package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
)

// userCore provisions the named user alone, the way Xray's AlterInbound does.
type userCore struct {
	kind        core.Kind
	sawUsers    []core.User
	sawUser     core.User
	sawEmail    string
	sawSettings string
	addCalls    int
	dropCalls   int
}

func (c *userCore) Describe() core.Descriptor {
	return core.Descriptor{ID: c.kind, TitleKey: "core.user"}
}
func (c *userCore) Kinds() []core.Kind              { return []core.Kind{c.kind} }
func (c *userCore) Preflight(context.Context) error { return nil }

func (c *userCore) AddUser(_ context.Context, inst core.Instance, user core.User) error {
	c.addCalls++
	c.sawUsers, c.sawUser, c.sawSettings = inst.Users, user, inst.Settings
	return nil
}

func (c *userCore) RemoveUser(_ context.Context, inst core.Instance, email string) error {
	c.dropCalls++
	c.sawUsers, c.sawEmail, c.sawSettings = inst.Users, email, inst.Settings
	return nil
}

// setCore is modelled on mtg: it provisions by re-applying the whole set it is
// handed, so whatever is missing from that set is revoked.
type setCore struct{ userCore }

func (c *setCore) Describe() core.Descriptor {
	return core.Descriptor{ID: c.kind, TitleKey: "core.set"}
}
func (c *setCore) ProvisionsWholeUserSet() {}

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

// localFor wires a Local whose reload counter the caller can read, so a test can
// tell "applied the row as it stands" from "never had to ask for it".
func localFor(t *testing.T, c core.Core, current *model.Inbound) (*Local, *int) {
	t.Helper()
	registry := core.NewRegistry()
	if err := registry.Register(c); err != nil {
		t.Fatalf("register: %v", err)
	}
	reloads := 0
	return NewLocal(LocalDeps{
		Cores: registry,
		LoadInbound: func(int) (*model.Inbound, error) {
			reloads++
			return current, nil
		},
	}), &reloads
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
		c := &setCore{userCore{kind: "setcore"}}
		l, _ := localFor(t, c, current)
		if err := l.AddUser(t.Context(), stale, "b@x"); err != nil {
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
		c := &setCore{userCore{kind: "setcore"}}
		l, _ := localFor(t, c, current)
		if err := l.RemoveUser(t.Context(), stale, "a@x"); err != nil {
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

/*
A core that provisions the named user alone is handed that user and nothing else.

Rebuilding the whole inbound for it re-read the row and walked its clients blob
twice — healing it, then projecting every client — which measured 1.3s of CPU on
a 200k-client inbound, paid once per client by every bulk add, enable and delete.
*/
func TestAOneUserCoreIsNotHandedTheWholeInbound(t *testing.T) {
	// A stale client method is what makes healing rewrite this blob, so a healed
	// instance is distinguishable from the stored one.
	stored := `{"method":"aes-256-gcm","clients":[` +
		`{"email":"a@x","password":"pw-a","method":"chacha20-ietf-poly1305"},` +
		`{"email":"b@x","password":"pw-b"}]}`
	ib := &model.Inbound{
		Id: 7, Protocol: model.Shadowsocks, Tag: "in-7", Port: 443, Enable: true,
		Settings: stored,
	}
	// Reloading would be visible: this row carries a client the caller's does not.
	current := &model.Inbound{
		Id: 7, Protocol: model.Shadowsocks, Tag: "in-7", Port: 443, Enable: true,
		Settings: `{"method":"aes-256-gcm","clients":[{"email":"a@x"},{"email":"b@x"},{"email":"c@x"}]}`,
	}

	t.Run("add", func(t *testing.T) {
		c := &userCore{kind: core.Kind(model.Shadowsocks)}
		l, reloads := localFor(t, c, current)
		if err := l.AddUser(t.Context(), ib, "b@x"); err != nil {
			t.Fatalf("AddUser: %v", err)
		}
		if *reloads != 0 {
			t.Errorf("the row was re-read %d time(s); a core provisioning one user reads no other client, so the caller's copy is enough", *reloads)
		}
		if got, want := strings.Join(emailsOf(c.sawUsers), ","), "b@x"; got != want {
			t.Errorf("the core was handed the set %q, want %q — only the client it was told to add", got, want)
		}
		if c.sawUser.Email != "b@x" || c.sawUser.Credentials["password"] != "pw-b" {
			t.Errorf("added user = %q password=%v, want b@x/pw-b", c.sawUser.Email, c.sawUser.Credentials["password"])
		}
		if c.sawSettings != stored {
			t.Errorf("the core was handed settings %q, want the stored %q — healing walks every client to reach a top-level field", c.sawSettings, stored)
		}
	})

	t.Run("remove", func(t *testing.T) {
		c := &userCore{kind: core.Kind(model.Shadowsocks)}
		l, reloads := localFor(t, c, current)
		if err := l.RemoveUser(t.Context(), ib, "a@x"); err != nil {
			t.Fatalf("RemoveUser: %v", err)
		}
		if *reloads != 0 {
			t.Errorf("the row was re-read %d time(s); a removal names its client and describes none", *reloads)
		}
		if len(c.sawUsers) != 0 {
			t.Errorf("the core was handed %d projected client(s), want none — a removal reads no client", len(c.sawUsers))
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
				l, _ := localFor(t, &mutedCore{kind: "setcore"}, ib)
				return l.AddUser(t.Context(), ib, "a@x")
			},
			want: "cannot provision",
		},
		{
			name: "the inbound does not carry the client",
			run: func(t *testing.T) error {
				l, _ := localFor(t, &setCore{userCore{kind: "setcore"}}, ib)
				return l.AddUser(t.Context(), ib, "ghost@x")
			},
			want: "ghost@x",
		},
		{
			name: "the row cannot be re-read",
			run: func(t *testing.T) error {
				registry := core.NewRegistry()
				if err := registry.Register(&setCore{userCore{kind: "setcore"}}); err != nil {
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
