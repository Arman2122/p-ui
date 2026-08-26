package sub

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
The three renderers must agree about a VLESS inbound that stores no encryption.

A row created through the panel form always carries "none" (inbound-defaults.ts
seeds it), so this is reachable through /panel/api/inbounds/add with hand-written
settings, or by restoring an older panel's backup. The three renderers then
answered differently: the raw link omits the key, the Clash proxy drops it, and
the JSON config emitted `"encryption": ""`.

F018 filed that as an outage, on the reading that the subscriber's xray refuses
an empty encryption. It does not, on the core this panel pins: the only place
proxy/vless/outbound reads the field is outbound.go:87,
`if a.Encryption != "" && a.Encryption != "none"`, which treats the empty string
exactly as "none". There is no config-time check on it either. So this is a
renderer disagreement, not a failure to start, and the fix is worth having for
that reason alone — see RUNTIME-RESULTS section 26.
*/

func jsonEncryptionInbound(settings string) *model.Inbound {
	return &model.Inbound{
		Listen:         "203.0.113.20",
		Port:           443,
		Protocol:       model.VLESS,
		Remark:         "enc",
		Settings:       settings,
		StreamSettings: `{"network":"tcp","security":"none","tcpSettings":{"header":{"type":"none"}}}`,
	}
}

// The generated VLESS outbound uses xray's flat style: address, port, id and
// encryption sit directly under settings, with no vnext/users nesting.
func vlessOutboundSettings(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var outbound struct {
		Settings map[string]any `json:"settings"`
	}
	if err := json.Unmarshal(raw, &outbound); err != nil {
		t.Fatalf("read the generated outbound: %v\n%s", err, raw)
	}
	if outbound.Settings == nil {
		t.Fatalf("the generated outbound has no settings: %s", raw)
	}
	return outbound.Settings
}

func TestJsonConfigNeverEmitsAnEmptyVlessEncryption(t *testing.T) {
	cases := []struct {
		name     string
		settings string
	}{
		{"no encryption key at all", `{"clients":[{"id":"11111111-2222-4333-8444-555555555555","email":"user"}]}`},
		{"an empty encryption", `{"clients":[{"id":"11111111-2222-4333-8444-555555555555","email":"user"}],"encryption":""}`},
		{"whitespace, which is the same nothing", `{"clients":[{"id":"11111111-2222-4333-8444-555555555555","email":"user"}],"encryption":"   "}`},
	}

	subReq := NewSubService("")
	js := NewSubJsonService("", "", "", subReq)

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			inbound := jsonEncryptionInbound(c.settings)
			client, ok := subReq.clientForLink(inbound, "user")
			if !ok {
				t.Fatal("the fixture's client did not resolve")
			}
			user := vlessOutboundSettings(t, json.RawMessage(js.genVless(subReq, inbound, nil, client, "")))
			if got := user["encryption"]; got != "none" {
				t.Fatalf("encryption = %#v, want \"none\": the raw link omits the key and Clash drops it, so this renderer was the only one handing out a value the operator never chose", got)
			}
		})
	}
}

// A configured encryption is passed through untouched: the default is only for
// the absent case, and rewriting a real value would be a different defect.
func TestJsonConfigPassesAConfiguredVlessEncryptionThrough(t *testing.T) {
	const auth = "mlkem768x25519plus.native.600s.QEn4fVtsjlysseKSxOXb9Ce9iyLqAIuaDGWLGcWMSlU"

	subReq := NewSubService("")
	js := NewSubJsonService("", "", "", subReq)
	inbound := jsonEncryptionInbound(
		`{"clients":[{"id":"11111111-2222-4333-8444-555555555555","email":"user"}],"encryption":"` + auth + `"}`)

	client, ok := subReq.clientForLink(inbound, "user")
	if !ok {
		t.Fatal("the fixture's client did not resolve")
	}
	user := vlessOutboundSettings(t, json.RawMessage(js.genVless(subReq, inbound, nil, client, "")))
	if got, _ := user["encryption"].(string); !strings.HasPrefix(got, "mlkem768") {
		t.Fatalf("encryption = %q, want the configured value passed through", got)
	}
}
