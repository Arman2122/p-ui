package service

import (
	"encoding/json"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/runtime"
)

// wrappingCore mints a client key against the inbound's own server key, the
// shape OpenVPN's tls-crypt-v2 has: one such key is good on one inbound only.
type wrappingCore struct{ stubCore }

func (wrappingCore) ClientCredentials(core.Kind) []string {
	return []string{core.CredUsername, core.CredPassword, core.CredTLSCryptV2}
}

func (wrappingCore) MintClientCredentials(_ core.Kind, settings string, have map[string]string) (map[string]string, error) {
	var doc struct {
		ServerKey string `json:"serverKey"`
	}
	_ = json.Unmarshal([]byte(settings), &doc)
	minted := map[string]string{}
	if have[core.CredUsername] == "" {
		minted[core.CredUsername] = "login"
	}
	if have[core.CredPassword] == "" {
		minted[core.CredPassword] = "password"
	}
	if have[core.CredTLSCryptV2] == "" {
		minted[core.CredTLSCryptV2] = "wrapped-by-" + doc.ServerKey
	}
	return minted, nil
}

func (wrappingCore) ValidateClient(core.Kind, string, string, map[string]string) error { return nil }

func (wrappingCore) ClientIdentity(core.Kind) string { return "email" }

/*
A client added to two inbounds of a kind whose client key is wrapped by the
inbound's own key carries one key per inbound: the one the first inbound
minted must not ride into the second, on the same save, on a later one, or
through the bulk import.
*/
func TestInboundBoundCredentialIsMintedPerInbound(t *testing.T) {
	setupBulkDB(t)
	reg, err := cores.Default(cores.Deps{})
	if err != nil {
		t.Fatalf("default registry: %v", err)
	}
	stub := wrappingCore{stubCore{descriptor: core.Descriptor{ID: "wrapcore", TitleKey: "cores.wrap.title"}, kinds: []core.Kind{"wrapkind"}}}
	if err := reg.Register(stub); err != nil {
		t.Fatalf("register stub: %v", err)
	}
	cores.Use(reg)
	runtime.SetManager(runtime.NewManager(runtime.LocalDeps{Cores: reg}))

	svc := &ClientService{}
	inboundSvc := &InboundService{}
	wrapInbound := func(port int, key string) *model.Inbound {
		return mkInbound(t, port, model.Protocol("wrapkind"), `{"clients":[],"serverKey":"`+key+`"}`)
	}
	one, two := wrapInbound(24601, "k1"), wrapInbound(24602, "k2")
	if _, err := svc.Create(inboundSvc, &ClientCreatePayload{
		Client:     model.Client{Email: "wrap@x", SubID: "sub-wrap", Enable: true},
		InboundIds: []int{one.Id, two.Id},
	}); err != nil {
		t.Fatalf("create on two inbounds: %v", err)
	}
	expectWrapped(t, inboundSvc, "wrap@x", map[int]string{one.Id: "wrapped-by-k1", two.Id: "wrapped-by-k2"})
	if a, b := loginOn(t, inboundSvc, one.Id, "wrap@x"), loginOn(t, inboundSvc, two.Id, "wrap@x"); a != b {
		t.Fatalf("the login differs across inbounds: %q vs %q", a, b)
	}

	// The same client added later to a third inbound gets that inbound's key,
	// not one the record store kept from an earlier inbound.
	three := wrapInbound(24603, "k3")
	if _, err := svc.Create(inboundSvc, &ClientCreatePayload{
		Client:     model.Client{Email: "wrap@x", SubID: "sub-wrap", Enable: true},
		InboundIds: []int{three.Id},
	}); err != nil {
		t.Fatalf("create on a third inbound: %v", err)
	}
	expectWrapped(t, inboundSvc, "wrap@x", map[int]string{three.Id: "wrapped-by-k3"})

	four, five := wrapInbound(24604, "k4"), wrapInbound(24605, "k5")
	res, _, err := svc.BulkCreate(inboundSvc, []ClientCreatePayload{{
		Client:     model.Client{Email: "bulk@x", SubID: "sub-bulk", Enable: true},
		InboundIds: []int{four.Id, five.Id},
	}})
	if err != nil || res.Created != 1 {
		t.Fatalf("bulk create: created=%d skipped=%+v err=%v", res.Created, res.Skipped, err)
	}
	expectWrapped(t, inboundSvc, "bulk@x", map[int]string{four.Id: "wrapped-by-k4", five.Id: "wrapped-by-k5"})
}

func expectWrapped(t *testing.T, inboundSvc *InboundService, email string, want map[int]string) {
	t.Helper()
	for id, key := range want {
		if got := clientOn(t, inboundSvc, id, email).TLSCryptV2; got != key {
			t.Fatalf("inbound %d holds %q for %s, want %q", id, got, email, key)
		}
	}
}

func clientOn(t *testing.T, inboundSvc *InboundService, inboundId int, email string) model.Client {
	t.Helper()
	ib, err := inboundSvc.GetInbound(inboundId)
	if err != nil {
		t.Fatalf("GetInbound %d: %v", inboundId, err)
	}
	clients, err := inboundSvc.GetClients(ib)
	if err != nil {
		t.Fatalf("GetClients %d: %v", inboundId, err)
	}
	for _, c := range clients {
		if c.Email == email {
			return c
		}
	}
	t.Fatalf("inbound %d has no client %q", inboundId, email)
	return model.Client{}
}

func loginOn(t *testing.T, inboundSvc *InboundService, inboundId int, email string) string {
	t.Helper()
	c := clientOn(t, inboundSvc, inboundId, email)
	return c.Username + ":" + c.Password
}
