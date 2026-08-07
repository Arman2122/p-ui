package service

import (
	"encoding/json"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

// An inbound written with Vision on a transport that cannot carry it leaves xray
// expecting Vision while the generated link never sends it, so the client the
// panel just handed out connects to nothing.
func TestStripVisionFlowForIneligibleInbound(t *testing.T) {
	const vision = "xtls-rprx-vision"
	const enc = `"decryption":"mlkem768x25519plus.native.0rtt.KEY","encryption":"mlkem768x25519plus.native.0rtt.KEY",`
	withFlow := func(extra string) string {
		return `{` + extra + `"clients":[{"id":"u1","email":"a@x","flow":"` + vision + `","subId":"s1","enable":true}]}`
	}

	tests := []struct {
		name        string
		settings    string
		stream      string
		protocol    model.Protocol
		wantChanged bool
		wantFlow    string
	}{
		{"xhttp without vlessenc loses Vision", withFlow(""), `{"network":"xhttp","security":"reality"}`, model.VLESS, true, ""},
		{"xhttp with vlessenc keeps Vision", withFlow(enc), `{"network":"xhttp","security":"reality"}`, model.VLESS, false, vision},
		{"raw tcp over reality keeps Vision", withFlow(""), `{"network":"tcp","security":"reality"}`, model.VLESS, false, vision},
		{"non-VLESS is untouched", withFlow(""), `{"network":"xhttp","security":"reality"}`, model.VMESS, false, vision},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, changed := stripVisionFlowForIneligibleInbound(tc.settings, tc.stream, tc.protocol)
			if changed != tc.wantChanged {
				t.Fatalf("changed = %v, want %v", changed, tc.wantChanged)
			}
			var parsed map[string]any
			if err := json.Unmarshal([]byte(out), &parsed); err != nil {
				t.Fatalf("parse out: %v", err)
			}
			client, ok := parsed["clients"].([]any)[0].(map[string]any)
			if !ok {
				t.Fatal("client 0 is not an object")
			}
			got, _ := client["flow"].(string)
			if got != tc.wantFlow {
				t.Errorf("flow = %q, want %q", got, tc.wantFlow)
			}
		})
	}
}

// restoreVisionFlowForEligibleInbound must re-add Vision to a client whose flow
// was stripped while the XHTTP inbound was not yet vlessenc-encrypted, but only
// when the client's intended flow (its flow_override on a sibling) is Vision,
// only on now-eligible inbounds, and never overwriting an explicit flow.
func TestRestoreVisionFlowForEligibleInbound(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()

	const vision = "xtls-rprx-vision"
	const realityStream = `{"network":"tcp","security":"reality"}`
	const xhttpEnc = `{"network":"xhttp","security":"reality"}`
	const encSettings = `"decryption":"mlkem768x25519plus.native.0rtt.KEY","encryption":"mlkem768x25519plus.native.0rtt.KEY"`

	cs := &ClientService{}
	ibSvc := &InboundService{}

	// Sibling reality inbound where the client legitimately has Vision.
	sibling := &model.Inbound{
		Tag: "sib", Enable: true, Port: 51001, Protocol: model.VLESS, StreamSettings: realityStream,
		Settings: `{"clients":[{"id":"u1","email":"keep@x","flow":"` + vision + `","subId":"s1","enable":true}]}`,
	}
	if err := db.Create(sibling).Error; err != nil {
		t.Fatalf("create sibling: %v", err)
	}
	keep, _ := ibSvc.GetClients(sibling)
	if err := cs.SyncInbound(nil, sibling.Id, keep); err != nil {
		t.Fatalf("sync sibling: %v", err)
	}

	// A client with no intended Vision anywhere — must NOT be touched.
	other := &model.Inbound{
		Tag: "oth", Enable: true, Port: 51002, Protocol: model.VLESS, StreamSettings: realityStream,
		Settings: `{"clients":[{"id":"u2","email":"none@x","subId":"s2","enable":true}]}`,
	}
	if err := db.Create(other).Error; err != nil {
		t.Fatalf("create other: %v", err)
	}
	oc, _ := ibSvc.GetClients(other)
	if err := cs.SyncInbound(nil, other.Id, oc); err != nil {
		t.Fatalf("sync other: %v", err)
	}

	// The now-eligible XHTTP inbound: keep@x has empty flow (was stripped),
	// none@x has empty flow (no Vision anywhere), set@x has an explicit empty
	// stays empty unless intended Vision.
	target := `{` + encSettings + `,"clients":[` +
		`{"id":"u1","email":"keep@x","flow":"","subId":"s1","enable":true},` +
		`{"id":"u2","email":"none@x","flow":"","subId":"s2","enable":true}` +
		`]}`

	out, changed := ibSvc.restoreVisionFlowForEligibleInbound(nil, target, xhttpEnc, model.VLESS)
	if !changed {
		t.Fatal("expected changed=true")
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("parse out: %v", err)
	}
	flows := map[string]string{}
	for _, c := range parsed["clients"].([]any) {
		cm := c.(map[string]any)
		flows[cm["email"].(string)], _ = cm["flow"].(string)
	}
	if flows["keep@x"] != vision {
		t.Errorf("keep@x flow = %q, want Vision (intended on sibling)", flows["keep@x"])
	}
	if flows["none@x"] != "" {
		t.Errorf("none@x flow = %q, want empty (no Vision intent)", flows["none@x"])
	}

	// Ineligible inbound (xhttp without encryption) must be a no-op.
	noenc := `{"clients":[{"id":"u1","email":"keep@x","flow":"","subId":"s1","enable":true}]}`
	if _, ch := ibSvc.restoreVisionFlowForEligibleInbound(nil, noenc, `{"network":"xhttp","security":"reality"}`, model.VLESS); ch {
		t.Error("ineligible xhttp (no vlessenc) must not change")
	}
	// Non-VLESS must be a no-op.
	if _, ch := ibSvc.restoreVisionFlowForEligibleInbound(nil, target, xhttpEnc, model.VMESS); ch {
		t.Error("non-VLESS must not change")
	}
}
