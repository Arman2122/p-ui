package service

import (
	"encoding/json"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/mtproto"
)

/*
The inbound save path heals mtproto secrets. The client save path is the second
door into the same settings blob, and it went straight to tx.Save.

A malformed secret is not one broken client. The sidecar's PUT /secrets parses
every entry before applying any, and 400s the whole batch on the first bad one,
so no client's edit hot-applies; on a cold start Config.Validate refuses to run
at all. One field takes the inbound down.

Healed at the second door now, with the same protocol-gated normaliser the
first door uses. F172.
*/

func mtprotoSettings(t *testing.T, domain string, clients []model.Client) string {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(clientsSettings(t, clients)), &out); err != nil {
		t.Fatalf("unmarshal settings: %v", err)
	}
	out["fakeTlsDomain"] = domain
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	return string(b)
}

func storedSecret(t *testing.T, inboundId int, email string) string {
	t.Helper()
	var ib model.Inbound
	if err := database.GetDB().First(&ib, inboundId).Error; err != nil {
		t.Fatalf("read back inbound: %v", err)
	}
	var parsed struct {
		Clients []model.Client `json:"clients"`
	}
	if err := json.Unmarshal([]byte(ib.Settings), &parsed); err != nil {
		t.Fatalf("parse stored settings: %v\n%s", err, ib.Settings)
	}
	for _, c := range parsed.Clients {
		if c.Email == email {
			return c.Secret
		}
	}
	t.Fatalf("client %q is not in the stored settings: %s", email, ib.Settings)
	return ""
}

func TestEditingAnMtprotoClientCannotStoreAnUnusableSecret(t *testing.T) {
	setupBulkDB(t)
	svc := &ClientService{}
	inboundSvc := &InboundService{}

	const domain = "www.cloudflare.com"
	source := []model.Client{{
		Email: "mt-edit@x", SubID: "sub-mt-edit", Enable: true,
		Secret: model.GenerateFakeTLSSecret(domain),
	}}
	ib := mkInbound(t, 22701, model.MTProto, mtprotoSettings(t, domain, source))
	if err := svc.SyncInbound(nil, ib.Id, source); err != nil {
		t.Fatalf("seed linkage: %v", err)
	}

	broken := source
	broken[0].Secret = "not-hex"
	if _, err := svc.UpdateInboundClient(inboundSvc, &model.Inbound{
		Id:       ib.Id,
		Protocol: model.MTProto,
		Settings: mtprotoSettings(t, domain, broken),
	}, "mt-edit@x"); err != nil {
		t.Fatalf("UpdateInboundClient: %v", err)
	}

	got := storedSecret(t, ib.Id, "mt-edit@x")
	if got == "not-hex" {
		t.Fatal("the malformed secret round-tripped verbatim; the sidecar will 400 the whole PUT /secrets and refuse to start, taking every client of this inbound down")
	}
	if mtproto.UsableSecret(got) == "" {
		t.Fatalf("the stored secret is still not one the sidecar accepts: %q", got)
	}
}

// The add door is the same blob and the same skip.
func TestAddingAnMtprotoClientCannotStoreAnUnusableSecret(t *testing.T) {
	setupBulkDB(t)
	svc := &ClientService{}
	inboundSvc := &InboundService{}

	const domain = "www.cloudflare.com"
	existing := []model.Client{{
		Email: "mt-keep@x", SubID: "sub-mt-keep", Enable: true,
		Secret: model.GenerateFakeTLSSecret(domain),
	}}
	ib := mkInbound(t, 22702, model.MTProto, mtprotoSettings(t, domain, existing))
	if err := svc.SyncInbound(nil, ib.Id, existing); err != nil {
		t.Fatalf("seed linkage: %v", err)
	}

	added := []model.Client{{
		Email: "mt-new@x", SubID: "sub-mt-new", Enable: true, Secret: "not-hex",
	}}
	if _, err := svc.AddInboundClient(inboundSvc, &model.Inbound{
		Id:       ib.Id,
		Protocol: model.MTProto,
		Settings: mtprotoSettings(t, domain, added),
	}); err != nil {
		t.Fatalf("AddInboundClient: %v", err)
	}

	got := storedSecret(t, ib.Id, "mt-new@x")
	if mtproto.UsableSecret(got) == "" {
		t.Fatalf("a client added with a malformed secret was stored as-is: %q", got)
	}
	// And the sibling that was already fine is untouched.
	if kept := storedSecret(t, ib.Id, "mt-keep@x"); mtproto.UsableSecret(kept) == "" {
		t.Fatalf("healing the new client rewrote an existing one into something unusable: %q", kept)
	}
}
