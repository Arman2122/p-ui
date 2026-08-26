package service

import (
	"encoding/json"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
A Trojan client's flow must never reach the generated config.

The pinned core removed it: `infra/conf/trojan.go:74` answers a non-empty Flow
on a Trojan account with `PrintRemovedFeatureError`, and that fails the WHOLE
config — every inbound on the box, not just the Trojan one.

RenderInbound copied the client's flow into the Trojan user object whenever the
join carried one. Nothing panel-created writes that today, because
stripVisionFlowForIneligibleInbound gates every current save path — so the rows
this reaches are legacy or imported ones, which is exactly the population that
gets restored into a fresh panel and stops it starting. F323.

The stream here is tcp + reality, which is the shape that DOES allow a flow for
VLESS. That is the point: the rule this pins is the protocol half.
*/
func TestRenderInboundDropsATrojanClientFlow(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()

	inbound := durableTestInbound(nil, "trojan-flow", 25411)
	inbound.Protocol = model.Trojan
	inbound.Settings = `{"clients":[]}`
	inbound.StreamSettings = `{"network":"tcp","security":"reality","realitySettings":{"serverNames":["x.example"],"shortIds":["ab"],"settings":{"publicKey":"PBK","fingerprint":"chrome"}}}`
	if err := db.Create(inbound).Error; err != nil {
		t.Fatalf("seed inbound: %v", err)
	}

	client := &model.ClientRecord{Email: "legacy@e", Enable: true, Password: "p4ss"}
	if err := db.Create(client).Error; err != nil {
		t.Fatalf("seed client: %v", err)
	}
	if err := db.Create(&model.ClientInbound{
		ClientId: client.Id, InboundId: inbound.Id, FlowOverride: "xtls-rprx-vision",
	}).Error; err != nil {
		t.Fatalf("link client: %v", err)
	}

	rendered, err := (&XrayService{}).RenderInbound(inbound)
	if err != nil {
		t.Fatalf("RenderInbound: %v", err)
	}
	if rendered == nil {
		t.Fatal("nothing was rendered; this test is checking the wrong thing")
	}

	clients := renderedClients(t, rendered)
	if len(clients) != 1 {
		t.Fatalf("want the one seeded client, got %d: %v", len(clients), clients)
	}
	if _, present := clients[0]["flow"]; present {
		t.Fatalf("the rendered Trojan account carries flow=%v; the core refuses it and the whole config fails to load", clients[0]["flow"])
	}
	// Dropping the flow is not licence to drop the account.
	if clients[0]["password"] != "p4ss" {
		t.Fatalf("the account was lost along with its flow: %v", clients[0])
	}
}

// renderedClients digs the clients array out of a rendered inbound's settings,
// which is json.RawMessage rather than a map.
func renderedClients(t *testing.T, rendered any) []map[string]any {
	t.Helper()
	body, err := json.Marshal(rendered)
	if err != nil {
		t.Fatalf("marshal rendered inbound: %v", err)
	}
	var shape struct {
		Settings struct {
			Clients []map[string]any `json:"clients"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(body, &shape); err != nil {
		t.Fatalf("read back the rendered inbound: %v\n%s", err, body)
	}
	return shape.Settings.Clients
}
