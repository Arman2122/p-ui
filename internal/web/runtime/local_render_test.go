package runtime

import (
	"context"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
)

// applyCore converges whole instances and records the last one it was handed.
type applyCore struct {
	kind core.Kind
	saw  core.Instance
}

func (c *applyCore) Describe() core.Descriptor {
	return core.Descriptor{ID: c.kind, TitleKey: "core.apply"}
}
func (c *applyCore) Kinds() []core.Kind              { return []core.Kind{c.kind} }
func (c *applyCore) Preflight(context.Context) error { return nil }

func (c *applyCore) ApplyInstance(_ context.Context, inst core.Instance) error {
	c.saw = inst
	return nil
}

func (c *applyCore) DropInstance(context.Context, core.Instance) error { return nil }

// rendersWithout is a render that drops one client, the way the panel's drops
// a depleted one, and leaves everything else as stored.
func rendersWithout(email string) func(*model.Inbound) (*core.InboundConfig, error) {
	return func(ib *model.Inbound) (*core.InboundConfig, error) {
		kept := make([]core.User, 0)
		for _, u := range usersOf(ib.Settings, "") {
			if u.Email != email {
				kept = append(kept, u)
			}
		}
		parts := make([]string, 0, len(kept))
		for _, u := range kept {
			parts = append(parts, `{"email":"`+u.Email+`","secret":"`+u.Credentials["secret"].(string)+`","enable":true}`)
		}
		return &core.InboundConfig{Settings: []byte(`{"clients":[` + strings.Join(parts, ",") + `]}`)}, nil
	}
}

/*
An applied inbound carries the users of its render, not of its row.

The render is where a depleted client leaves the inbound. A core that reads
Instance.Users — every core but Xray — would otherwise be handed the client the
render just removed, and put it straight back.
*/
func TestAnAppliedInboundCarriesTheUsersOfItsRender(t *testing.T) {
	row := &model.Inbound{
		Id: 3, Protocol: "applycore", Tag: "in-3", Port: 443, Enable: true,
		Settings: `{"clients":[{"email":"a@x","secret":"ee01","enable":true},{"email":"cut@x","secret":"ee02","enable":true}]}`,
	}
	c := &applyCore{kind: "applycore"}
	registry := core.NewRegistry()
	if err := registry.Register(c); err != nil {
		t.Fatalf("register: %v", err)
	}
	l := NewLocal(LocalDeps{Cores: registry, RenderInbound: rendersWithout("cut@x")})
	if err := l.AddInbound(t.Context(), row); err != nil {
		t.Fatalf("AddInbound: %v", err)
	}
	if got, want := strings.Join(emailsOf(c.saw.Users), ","), "a@x"; got != want {
		t.Errorf("the core was handed users %q, want %q — Settings and Users must describe the same clients", got, want)
	}
	if strings.Contains(c.saw.Settings, "cut@x") {
		t.Errorf("the rendered settings still carry the dropped client: %s", c.saw.Settings)
	}
}

/*
A whole-set core is handed the inbound as the runtime renders it, not as the
row stores it. The row still lists a client depletion cut off; a core that
re-applies whatever it is handed would serve that client again on the next
unrelated user op.
*/
func TestAWholeSetCoreIsHandedTheRenderedInbound(t *testing.T) {
	current := &model.Inbound{
		Id: 7, Protocol: "setcore", Tag: "in-7", Port: 443, Enable: true,
		Settings: `{"clients":[{"email":"a@x","secret":"ee01","enable":true},{"email":"cut@x","secret":"ee02","enable":true}]}`,
	}
	for _, op := range []string{"add", "remove"} {
		t.Run(op, func(t *testing.T) {
			c := &setCore{userCore{kind: "setcore"}}
			registry := core.NewRegistry()
			if err := registry.Register(c); err != nil {
				t.Fatalf("register: %v", err)
			}
			l := NewLocal(LocalDeps{
				Cores:         registry,
				LoadInbound:   func(int) (*model.Inbound, error) { return current, nil },
				RenderInbound: rendersWithout("cut@x"),
			})
			var err error
			if op == "add" {
				err = l.AddUser(t.Context(), current, "a@x")
			} else {
				err = l.RemoveUser(t.Context(), current, "a@x")
			}
			if err != nil {
				t.Fatalf("%s: %v", op, err)
			}
			if got, want := strings.Join(emailsOf(c.sawUsers), ","), "a@x"; got != want {
				t.Errorf("the core was handed the set %q, want %q — a client the render dropped must not come back through a user op", got, want)
			}
		})
	}
}
