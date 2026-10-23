package service

import (
	"encoding/json"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestPortableOrphanImportPreservesDisabledState(t *testing.T) {
	setupBulkDB(t)
	result, _, err := (&ClientService{}).ImportClients(&InboundService{}, []ClientCreatePayload{{
		Client: model.Client{Email: "disabled-orphan", ID: "11111111-1111-1111-1111-111111111111", SubID: "disabled-orphan-sub", Enable: false},
	}})
	if err != nil || result.Created != 1 {
		t.Fatalf("import = %+v, error = %v", result, err)
	}
	if got := lookupClientRecord(t, "disabled-orphan"); got.Enable {
		t.Fatal("import silently enabled the disabled orphan")
	}
}

func TestAddInboundAcceptsLegacyClientNumbers(t *testing.T) {
	setupBulkDB(t)
	inbound := &model.Inbound{
		UserId: 1, Tag: "legacy-import", Port: 47511, Protocol: model.VLESS, Enable: false,
		Settings:       `{"decryption":"none","clients":[{"id":"11111111-1111-1111-1111-111111111111","email":"legacy-client","enable":false,"tgId":"","limitIp":"3","totalGB":"4096","expiryTime":"1700000000000","reset":"7"}]}`,
		StreamSettings: `{"network":"tcp","security":"none"}`,
	}
	if _, _, _, err := (&InboundService{}).AddInbound(inbound); err != nil {
		t.Fatal(err)
	}
	stored, err := (&InboundService{}).GetInbound(inbound.Id)
	if err != nil {
		t.Fatal(err)
	}
	clients, err := (&InboundService{}).GetClients(stored)
	if err != nil || len(clients) != 1 {
		t.Fatalf("clients=%v error=%v", clients, err)
	}
	client := clients[0]
	if client.TgID != 0 || client.LimitIP != 3 || client.TotalGB != 4096 || client.ExpiryTime != 1700000000000 || client.Reset != 7 {
		t.Fatalf("legacy numeric fields = %+v", client)
	}
	var parsed struct {
		Clients []map[string]any `json:"clients"`
	}
	if err := json.Unmarshal([]byte(stored.Settings), &parsed); err != nil {
		t.Fatal(err)
	}
	if _, ok := parsed.Clients[0]["limitIp"].(float64); !ok {
		t.Fatalf("stored limitIp is not normalized: %v", parsed.Clients[0]["limitIp"])
	}
}

func TestInboundListUsesCanonicalBoundClientIdentity(t *testing.T) {
	setupBulkDB(t)
	const canonical = "11111111-1111-1111-1111-111111111111"
	ib := mkInbound(t, 47512, model.VLESS, `{"clients":[]}`)
	client := model.Client{Email: "identity@list", ID: canonical, SubID: "identity-list-sub", Enable: true}
	if _, err := (&ClientService{}).Create(&InboundService{}, &ClientCreatePayload{Client: client, InboundIds: []int{ib.Id}}); err != nil {
		t.Fatal(err)
	}
	client.ID = "99999999-9999-9999-9999-999999999999"
	if err := database.GetDB().Model(ib).Update("settings", clientsSettings(t, []model.Client{client})).Error; err != nil {
		t.Fatal(err)
	}
	rows, err := (&InboundService{}).GetAllInbounds()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Id != ib.Id {
			continue
		}
		if len(row.ClientStats) != 1 || row.ClientStats[0].UUID != canonical {
			t.Fatalf("listed identity = %+v, want %s", row.ClientStats, canonical)
		}
		return
	}
	t.Fatal("inbound missing from list")
}

func TestMixedCaseLoopbackFollowsInboundTagChanges(t *testing.T) {
	setupSettingTestDB(t)
	seedXrayTemplate(t, `{"routing":{"rules":[]},"outbounds":[{"tag":"loop","protocol":"Loopback","settings":{"inboundTag":"old-tag"}}]}`)
	svc := &XraySettingService{}
	if changed, err := svc.PropagateInboundTagRename("old-tag", "new-tag"); err != nil || !changed {
		t.Fatalf("rename changed=%v error=%v", changed, err)
	}
	raw, err := svc.GetXrayConfigTemplate()
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatal(err)
	}
	settings := outboundsFromCfg(config)[0].(map[string]any)["settings"].(map[string]any)
	if settings["inboundTag"] != "new-tag" {
		t.Fatalf("renamed loopback = %v", settings)
	}
	if changed, err := svc.RemoveInboundTagReferences("new-tag"); err != nil || !changed {
		t.Fatalf("remove changed=%v error=%v", changed, err)
	}
	raw, err = svc.GetXrayConfigTemplate()
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatal(err)
	}
	settings = outboundsFromCfg(config)[0].(map[string]any)["settings"].(map[string]any)
	if _, exists := settings["inboundTag"]; exists {
		t.Fatalf("deleted tag retained in loopback = %v", settings)
	}
}
