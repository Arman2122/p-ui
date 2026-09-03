package service

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/runtime"
)

// wrappingCore mints a client key against the inbound's own server key, the
// shape OpenVPN's tls-crypt-v2 has: one such key is good on one inbound only,
// and every mint is a new key, so a re-mint is visible.
type wrappingCore struct {
	stubCore
	mints atomic.Int64
}

func (*wrappingCore) ClientCredentials(core.Kind) []string {
	return []string{core.CredUsername, core.CredPassword, core.CredTLSCryptV2}
}

func (c *wrappingCore) MintClientCredentials(_ core.Kind, settings string, have map[string]string) (map[string]string, error) {
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
		minted[core.CredTLSCryptV2] = "wrapped-by-" + doc.ServerKey + "#" + strconv.FormatInt(c.mints.Add(1), 10)
	}
	return minted, nil
}

func (*wrappingCore) ValidateClient(core.Kind, string, string, map[string]string) error { return nil }

func (*wrappingCore) ClientIdentity(core.Kind) string { return "email" }

/*
A client on several inbounds of a kind whose client key is wrapped by the
inbound's own key carries one key per inbound: the one the first inbound
minted must not ride into the second on the same save, on a later add or
through the bulk import, and a partial edit that omits the key keeps every
inbound's own rather than minting again.
*/
func TestInboundBoundCredentialIsMintedPerInbound(t *testing.T) {
	setupBulkDB(t)
	reg, err := cores.Default(cores.Deps{})
	if err != nil {
		t.Fatalf("default registry: %v", err)
	}
	stub := &wrappingCore{stubCore: stubCore{descriptor: core.Descriptor{ID: "wrapcore", TitleKey: "cores.wrap.title"}, kinds: []core.Kind{"wrapkind"}}}
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
	keys := expectWrappedBy(t, inboundSvc, "wrap@x", map[int]string{one.Id: "k1", two.Id: "k2"})
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
	keys[three.Id] = expectWrappedBy(t, inboundSvc, "wrap@x", map[int]string{three.Id: "k3"})[three.Id]

	// A partial edit that omits the key keeps every inbound's own, unchanged.
	rec := lookupClientRecord(t, "wrap@x")
	if _, err := svc.Update(inboundSvc, rec.Id, model.Client{Email: "wrap@x", SubID: "sub-wrap", Enable: true, Comment: "renamed"}); err != nil {
		t.Fatalf("partial update: %v", err)
	}
	for id, key := range keys {
		if got := clientOn(t, inboundSvc, id, "wrap@x").TLSCryptV2; got != key {
			t.Fatalf("a partial edit changed inbound %d's key: %q -> %q", id, key, got)
		}
	}

	four, five := wrapInbound(24604, "k4"), wrapInbound(24605, "k5")
	res, _, err := svc.BulkCreate(inboundSvc, []ClientCreatePayload{{
		Client:     model.Client{Email: "bulk@x", SubID: "sub-bulk", Enable: true},
		InboundIds: []int{four.Id, five.Id},
	}})
	if err != nil || res.Created != 1 {
		t.Fatalf("bulk create: created=%d skipped=%+v err=%v", res.Created, res.Skipped, err)
	}
	expectWrappedBy(t, inboundSvc, "bulk@x", map[int]string{four.Id: "k4", five.Id: "k5"})

	// The bulk import re-adding a known email to one more inbound mints for it.
	six := wrapInbound(24606, "k6")
	res, _, err = svc.BulkCreate(inboundSvc, []ClientCreatePayload{{
		Client:     model.Client{Email: "wrap@x", SubID: "sub-wrap", Enable: true},
		InboundIds: []int{six.Id},
	}})
	if err != nil || res.Created != 1 {
		t.Fatalf("bulk re-add: created=%d skipped=%+v err=%v", res.Created, res.Skipped, err)
	}
	expectWrappedBy(t, inboundSvc, "wrap@x", map[int]string{six.Id: "k6"})
}

// expectWrappedBy checks each inbound holds a key wrapped by its own server
// key and returns the keys, so a later step can see they did not move.
func expectWrappedBy(t *testing.T, inboundSvc *InboundService, email string, want map[int]string) map[int]string {
	t.Helper()
	out := map[int]string{}
	for id, serverKey := range want {
		got := clientOn(t, inboundSvc, id, email).TLSCryptV2
		if !strings.HasPrefix(got, "wrapped-by-"+serverKey+"#") {
			t.Fatalf("inbound %d holds %q for %s, want one wrapped by %s", id, got, email, serverKey)
		}
		out[id] = got
	}
	return out
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
