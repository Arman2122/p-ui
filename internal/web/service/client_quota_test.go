package service

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/runtime"
)

// convergedCore is a core the panel converges directly — a daemon with its
// own user list and its own byte counter, the shape mtg, SSH and ocserv have.
type convergedCore struct {
	stubCore
	mu     sync.Mutex
	mints  atomic.Int64
	added  []core.User
	resets []string
}

func (*convergedCore) ClientCredentials(core.Kind) []string { return []string{core.CredPassword} }

func (c *convergedCore) MintClientCredentials(_ core.Kind, _ string, have map[string]string) (map[string]string, error) {
	if have[core.CredPassword] != "" {
		return map[string]string{}, nil
	}
	return map[string]string{core.CredPassword: "pw-" + strconv.FormatInt(c.mints.Add(1), 10)}, nil
}

func (*convergedCore) ValidateClient(_ core.Kind, _, _ string, have map[string]string) error {
	if have[core.CredPassword] == "refused" {
		return errors.New("convcore refuses that password")
	}
	return nil
}

func (*convergedCore) ClientIdentity(core.Kind) string { return "email" }

func (*convergedCore) ProvisionsUsers(core.Kind) bool { return true }

func (*convergedCore) RemoveUser(context.Context, core.Instance, string) error { return nil }

func (c *convergedCore) AddUser(_ context.Context, _ core.Instance, user core.User) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.added = append(c.added, user)
	return nil
}

func (c *convergedCore) ResetQuota(_ context.Context, email string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resets = append(c.resets, email)
	return nil
}

func (c *convergedCore) lastPassword() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.added) == 0 {
		return ""
	}
	pw, _ := c.added[len(c.added)-1].Credentials[core.CredPassword].(string)
	return pw
}

func (c *convergedCore) resetsSeen() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.resets)
}

func (c *convergedCore) forget() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.resets = nil
}

// useConvergedCore registers the fake beside the real cores and wires the
// runtime the panel's own wiring builds, so an op reaches it as it would mtg.
func useConvergedCore(t *testing.T) *convergedCore {
	t.Helper()
	reg, err := cores.Default(cores.Deps{})
	if err != nil {
		t.Fatalf("default registry: %v", err)
	}
	fake := &convergedCore{stubCore: stubCore{descriptor: core.Descriptor{ID: "convcore", TitleKey: "cores.conv.title"}, kinds: []core.Kind{"convkind"}}}
	if err := reg.Register(fake); err != nil {
		t.Fatalf("register fake: %v", err)
	}
	cores.Use(reg)
	inboundSvc := &InboundService{}
	runtime.SetManager(runtime.NewManager(runtime.LocalDeps{
		Cores:         reg,
		LoadInbound:   inboundSvc.GetInbound,
		RenderInbound: inboundSvc.RenderForRuntime,
	}))
	t.Cleanup(func() { runtime.SetManager(nil) })
	return fake
}

func storedPassword(t *testing.T, inboundSvc *InboundService, ib *model.Inbound, email string) string {
	t.Helper()
	fresh, err := inboundSvc.GetInbound(ib.Id)
	if err != nil {
		t.Fatalf("reload inbound: %v", err)
	}
	clients, err := inboundSvc.GetClients(fresh)
	if err != nil {
		t.Fatalf("clients: %v", err)
	}
	for _, c := range clients {
		if c.Email == email {
			return c.Password
		}
	}
	t.Fatalf("client %s not on inbound %d", email, ib.Id)
	return ""
}

/*
A bulk re-key on a local inbound reaches the core that serves it. Only mtproto
was re-applied after a rotate; every other core the panel converges kept
serving the old key until its next reconcile, and the share link the operator
just handed out did not work until then.
*/
func TestABulkReKeyReachesTheCoreThePanelConverges(t *testing.T) {
	setupBulkDB(t)
	fake := useConvergedCore(t)
	svc, inboundSvc := &ClientService{}, &InboundService{}
	ib := mkInbound(t, 24701, model.Protocol("convkind"), `{"clients":[]}`)
	if _, err := svc.Create(inboundSvc, &ClientCreatePayload{
		Client:     model.Client{Email: "key@x", SubID: "sub-key", Enable: true},
		InboundIds: []int{ib.Id},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	before := fake.lastPassword()
	if before == "" {
		t.Fatal("the create never reached the core; the rest proves nothing")
	}

	if _, _, err := svc.BulkRotate(inboundSvc, []string{"key@x"}, true, false); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	stored := storedPassword(t, inboundSvc, ib, "key@x")
	if stored == before {
		t.Fatalf("the rotate stored the same password %q; the test cannot see a re-key", stored)
	}
	if got := fake.lastPassword(); got != stored {
		t.Fatalf("after the rotate the core last saw password %q, the panel stores %q — the re-keyed client never reached the core, which serves the old key until its next reconcile", got, stored)
	}
}

/*
A traffic reset clears the daemon-side counter through the registry: the
per-inbound reset and the panel-wide one both asked mtproto by name, so a
client on any other quota-enforcing core stayed blocked by its daemon on a
budget the panel had just renewed.
*/
func TestATrafficResetClearsTheDaemonCounterThroughTheRegistry(t *testing.T) {
	setupBulkDB(t)
	fake := useConvergedCore(t)
	svc, inboundSvc := &ClientService{}, &InboundService{}
	ib := mkInbound(t, 24702, model.Protocol("convkind"), `{"clients":[]}`)
	if _, err := svc.Create(inboundSvc, &ClientCreatePayload{
		Client:     model.Client{Email: "quota@x", SubID: "sub-quota", Enable: true},
		InboundIds: []int{ib.Id},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	t.Run("perInbound", func(t *testing.T) {
		fake.forget()
		if _, err := inboundSvc.ResetClientTraffic(ib.Id, "quota@x"); err != nil {
			t.Fatalf("ResetClientTraffic: %v", err)
		}
		if got := fake.resetsSeen(); !slices.Contains(got, "quota@x") {
			t.Fatalf("the core was asked to reset %v, want quota@x among them — the daemon keeps counting against a budget the panel just renewed", got)
		}
	})

	t.Run("panelWide", func(t *testing.T) {
		fake.forget()
		if _, err := svc.ResetAllTraffics(inboundSvc); err != nil {
			t.Fatalf("ResetAllTraffics: %v", err)
		}
		if got := fake.resetsSeen(); !slices.Contains(got, "quota@x") {
			t.Fatalf("the core was asked to reset %v, want quota@x among them", got)
		}
	})
}

/*
A client edit is validated by its core as an add is. The edit path kept one
check of its own — mtproto's ad tag — and asked no core anything else, so a
client its core could not serve was stored and pushed.
*/
func TestAClientEditIsValidatedByItsCore(t *testing.T) {
	setupBulkDB(t)
	useConvergedCore(t)
	svc, inboundSvc := &ClientService{}, &InboundService{}
	ib := mkInbound(t, 24703, model.Protocol("convkind"), `{"clients":[]}`)
	if _, err := svc.Create(inboundSvc, &ClientCreatePayload{
		Client:     model.Client{Email: "edit@x", SubID: "sub-edit", Enable: true},
		InboundIds: []int{ib.Id},
	}); err != nil {
		t.Fatalf("create: %v", err)
	}
	before := storedPassword(t, inboundSvc, ib, "edit@x")
	rec, err := svc.GetRecordByEmail(nil, "edit@x")
	if err != nil {
		t.Fatalf("record: %v", err)
	}

	_, err = svc.Update(inboundSvc, rec.Id, model.Client{Email: "edit@x", SubID: "sub-edit", Enable: true, Password: "refused"})
	if err == nil || !strings.Contains(err.Error(), "refuses that password") {
		t.Fatalf("Update = %v, want the core's refusal", err)
	}
	if got := storedPassword(t, inboundSvc, ib, "edit@x"); got != before {
		t.Fatalf("the refused edit was stored: password %q, want %q", got, before)
	}
}
