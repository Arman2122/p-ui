package sub

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
The Clients page reads what the subscription reads.

It used to assemble the .conf in the browser from the handful of WireGuard fields
InboundOption carries — no Jc, no S1..S4, no I-tags, no header keys, because that
struct has nowhere to put them. So an operator who applied the obfuscation preset
and then downloaded a client's config from the Clients page got a plain WireGuard
tunnel: the handshake never completes when one side obfuscates and the other does
not, and neither side says anything.

This is the door the panel now uses, and it goes through the core that owns the
kind — so both surfaces hand out the same bytes.
*/
func TestPanelConfigFileCarriesTheObfuscation(t *testing.T) {
	initSubDB(t)
	db := database.GetDB()

	clients := []map[string]any{{
		"email": "u@awg", "enable": true, "subId": "panelfile",
		"privateKey": "clientPrivateKey=", "allowedIPs": []string{"10.9.0.4/32"},
	}}
	settings, err := json.Marshal(map[string]any{
		"secretKey": fileShareServerKey,
		"dns":       "9.9.9.9",
		"clients":   clients,
		"awg":       map[string]any{"jc": 4, "jmin": 40, "jmax": 70, "s1": 20, "s2": 30},
	})
	if err != nil {
		t.Fatal(err)
	}
	in := &model.Inbound{Port: 51821, Protocol: model.AWGKernel, Enable: true, Tag: "awg-file", Settings: string(settings)}
	if err := db.Create(in).Error; err != nil {
		t.Fatalf("create inbound: %v", err)
	}
	rec := &model.ClientRecord{Email: "u@awg", SubID: "panelfile", Enable: true, PrivateKey: "clientPrivateKey=", AllowedIPs: "10.9.0.4/32"}
	if err := db.Create(rec).Error; err != nil {
		t.Fatalf("create client: %v", err)
	}
	if err := db.Create(&model.ClientInbound{ClientId: rec.Id, InboundId: in.Id}).Error; err != nil {
		t.Fatalf("create link: %v", err)
	}

	filename, body, err := NewLinkProvider().ConfigFileForSubId("example.com", "panelfile", in.Id)
	if err != nil {
		t.Fatalf("ConfigFileForSubId: %v", err)
	}
	if !strings.HasSuffix(filename, ".conf") {
		t.Fatalf("filename = %q, want a .conf the client can import", filename)
	}
	for _, want := range []string{
		"[Interface]", "PrivateKey = clientPrivateKey=", "DNS = 9.9.9.9",
		"Jc = 4", "Jmin = 40", "Jmax = 70", "S1 = 20", "S2 = 30",
		"[Peer]", "Endpoint = example.com:51821",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the config the Clients page serves is missing %q:\n%s", want, body)
		}
	}
	// The obfuscation belongs to the interface: a key after [Peer] is read as the
	// peer's, and quietly ignored.
	if strings.Index(body, "Jc = 4") > strings.Index(body, "[Peer]") {
		t.Error("the parameters landed after [Peer], where the client will not read them")
	}
}
