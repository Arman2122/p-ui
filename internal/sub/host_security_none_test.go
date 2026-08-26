package sub

import (
	"encoding/json"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
A host that forces plaintext must leave no security block behind.

The JSON renderer's "none" arm dropped tlsSettings and left realitySettings in
place; the Clash renderer beside it drops both. So the same host, on the same
inbound, produced two different stream blocks depending on which subscription
format the client asked for.

Inert rather than dangerous, and worth saying so: the pinned core's
StreamConfig.Build has an empty arm for security "none", so the leftover block
was dead payload rather than a config the client acted on, and it carries only
the public half of a REALITY keypair — the private key never reaches a renderer.
This is renderer parity, which is the thing the corpus exists to enforce. F148.
*/
func TestJsonConfigDropsRealityWhenAHostForcesNone(t *testing.T) {
	subReq := NewSubService("")
	js := NewSubJsonService("", "", "", subReq)

	inbound := &model.Inbound{
		Listen:   "203.0.113.21",
		Port:     443,
		Protocol: model.VLESS,
		Remark:   "reality-none",
		Settings: `{"clients":[{"id":"11111111-2222-4333-8444-555555555555","email":"user"}],"decryption":"none"}`,
		StreamSettings: `{"network":"tcp","security":"reality",` +
			`"realitySettings":{"serverNames":["r.example"],"shortIds":["ab"],"settings":{"publicKey":"PBK","fingerprint":"chrome"}},` +
			`"externalProxy":[{"isHost":true,"forceTls":"none","dest":"plain.example","port":8080,"remark":"N"}]}`,
	}

	configs := js.getConfig(subReq, inbound, model.Client{ID: "11111111-2222-4333-8444-555555555555", Email: "user"}, "req.example")
	if len(configs) != 1 {
		t.Fatalf("want one profile, got %d", len(configs))
	}

	var profile struct {
		Outbounds []struct {
			StreamSettings map[string]any `json:"streamSettings"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal(configs[0], &profile); err != nil {
		t.Fatalf("read the profile: %v\n%s", err, configs[0])
	}
	stream := profile.Outbounds[0].StreamSettings
	if stream["security"] != "none" {
		t.Fatalf("the host forced none and security is %v", stream["security"])
	}
	if _, present := stream["tlsSettings"]; present {
		t.Fatalf("tlsSettings survived a none-forced host: %v", stream)
	}
	if _, present := stream["realitySettings"]; present {
		t.Fatalf("realitySettings survived a none-forced host, while the Clash renderer drops it — the two formats disagree about the same host: %v", stream)
	}
}
