package service

import (
	"encoding/json"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	wgutil "github.com/Arman2122/p-ui/internal/util/wireguard"
)

/*
A pre-shared key could be set in the client editor and never removed.

Every credential on model.Client is preserved when a partial update omits it —
that is what stops a caller adjusting only a quota from silently rotating a
client's uuid. While PreSharedKey was a plain string, "the caller did not send
this" and "the caller sent it empty" were the same value, so the update path
restored the stored key over an emptied field every time. The operator cleared
it, saved, reopened the client, and there it was.

It is a pointer now: nil is omitted, a pointer to empty is a deliberate clear.
The three cases below are the whole contract. F179.
*/
func TestAPreSharedKeyCanBeCleared(t *testing.T) {
	initTestDB(t)

	_, peerPublic, err := wgutil.GenerateWireguardKeypair()
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	const psk = "cHNrLWJhc2U2NC0zMmJ5dGVzLXBsYWNlaG9sZGVyISE="

	svc := &InboundService{}
	created, _, _, err := svc.AddInbound(&model.Inbound{
		UserId: 1, Enable: true, Port: 20171, Protocol: model.WGKernel, Tag: "psk-inbound",
		Listen: "0.0.0.0",
		Settings: `{"secretKey":"` + wgTestSecretKey() + `","mtu":1420,"address":["10.93.0.1/24"],` +
			`"clients":[{"email":"psk@wg","publicKey":"` + peerPublic + `","preSharedKey":"` + psk +
			`","allowedIPs":["10.93.0.2/32"]}]}`,
	})
	if err != nil {
		t.Fatalf("AddInbound: %v", err)
	}
	if got := storedPSK(t, created.Id); got != psk {
		t.Fatalf("seeded key = %q, want the one that was written", got)
	}

	apply := func(t *testing.T, field string) {
		t.Helper()
		payload := `{"clients":[{"email":"psk@wg","publicKey":"` + peerPublic + `"` + field +
			`,"allowedIPs":["10.93.0.2/32"]}]}`
		if _, err := (&ClientService{}).UpdateInboundClient(svc,
			&model.Inbound{Id: created.Id, Settings: payload}, "psk@wg"); err != nil {
			t.Fatalf("UpdateInboundClient: %v", err)
		}
	}

	// Omitted: the stored key is carried forward. This is the contract every
	// other credential relies on and it must not change.
	apply(t, "")
	if got := storedPSK(t, created.Id); got != psk {
		t.Fatalf("after an update that omitted the field, key = %q; omitting must preserve, or a partial update silently drops credentials", got)
	}

	// Sent empty: cleared, and the key is gone from the stored settings rather
	// than sitting there as an empty string.
	apply(t, `,"preSharedKey":""`)
	if got := storedPSK(t, created.Id); got != "" {
		t.Fatalf("after an update that sent the field EMPTY, key = %q; it can be set here and never removed", got)
	}
	if hasPSKField(t, created.Id) {
		t.Fatal("the cleared key is stored as an empty string; the settings should read like a client that never had one")
	}

	// And it can be set again afterwards.
	apply(t, `,"preSharedKey":"`+psk+`"`)
	if got := storedPSK(t, created.Id); got != psk {
		t.Fatalf("after setting it again, key = %q, want it back", got)
	}
}

func storedPSK(t *testing.T, inboundID int) string {
	t.Helper()
	clients := storedClients(t, inboundID)
	if len(clients) != 1 {
		t.Fatalf("want one client, got %d", len(clients))
	}
	psk, _ := clients[0]["preSharedKey"].(string)
	return psk
}

func hasPSKField(t *testing.T, inboundID int) bool {
	t.Helper()
	clients := storedClients(t, inboundID)
	if len(clients) != 1 {
		t.Fatalf("want one client, got %d", len(clients))
	}
	_, present := clients[0]["preSharedKey"]
	return present
}

func storedClients(t *testing.T, inboundID int) []map[string]any {
	t.Helper()
	var settings string
	if err := database.GetDB().Model(&model.Inbound{}).Where("id = ?", inboundID).
		Select("settings").Scan(&settings).Error; err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var parsed struct {
		Clients []map[string]any `json:"clients"`
	}
	if err := json.Unmarshal([]byte(settings), &parsed); err != nil {
		t.Fatalf("parse settings: %v", err)
	}
	return parsed.Clients
}
