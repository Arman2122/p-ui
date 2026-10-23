package service

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestBulkAttach_PreservesVisionFlowWhenCanonicalColumnZeroed(t *testing.T) {
	setupBulkDB(t)

	db := database.GetDB()

	const email = "vision-bulk@example.com"
	const uid = "ce8d33df-3a64-4f10-8f9b-91c3a8e0c222"
	const sub = "subvisionbulk0001"
	const vision = "xtls-rprx-vision"
	const realityStream = `{"network":"tcp","security":"reality"}`

	svc := ClientService{}
	source := model.Client{Email: email, ID: uid, SubID: sub, Enable: true, Flow: vision}

	reality1 := &model.Inbound{
		Tag: "vless-reality-bulk-1", Enable: true, Port: 42101, Protocol: model.VLESS,
		StreamSettings: realityStream,
		Settings:       clientsSettings(t, []model.Client{source}),
	}
	if err := db.Create(reality1).Error; err != nil {
		t.Fatalf("create reality1: %v", err)
	}
	reality2 := &model.Inbound{
		Tag: "vless-reality-bulk-2", Enable: true, Port: 42102, Protocol: model.VLESS,
		StreamSettings: realityStream, Settings: `{"clients":[]}`,
	}
	if err := db.Create(reality2).Error; err != nil {
		t.Fatalf("create reality2: %v", err)
	}
	wsTls := &model.Inbound{
		Tag: "vless-ws-bulk", Enable: true, Port: 42103, Protocol: model.VLESS,
		StreamSettings: `{"network":"ws","security":"tls"}`, Settings: `{"clients":[]}`,
	}
	if err := db.Create(wsTls).Error; err != nil {
		t.Fatalf("create ws: %v", err)
	}

	if err := svc.SyncInbound(nil, reality1.Id, []model.Client{clientWithInboundFlow(source, reality1)}); err != nil {
		t.Fatalf("SyncInbound(reality1): %v", err)
	}

	rec, err := svc.GetRecordByEmail(nil, email)
	if err != nil {
		t.Fatalf("GetRecordByEmail: %v", err)
	}
	if err := db.Model(&model.ClientRecord{}).Where("id = ?", rec.Id).Update("flow", "").Error; err != nil {
		t.Fatalf("zero canonical flow: %v", err)
	}

	inboundSvc := &InboundService{}
	if _, _, err := svc.BulkAttach(inboundSvc, []string{email}, []int{reality2.Id, wsTls.Id}); err != nil {
		t.Fatalf("BulkAttach: %v", err)
	}

	reality2List, err := svc.ListForInbound(nil, reality2.Id)
	if err != nil {
		t.Fatalf("ListForInbound(reality2): %v", err)
	}
	if len(reality2List) != 1 || reality2List[0].Flow != vision {
		t.Errorf("bulk-attached flow-capable inbound must inherit Vision via EffectiveFlow (#6432), got %#v", reality2List)
	}

	wsList, err := svc.ListForInbound(nil, wsTls.Id)
	if err != nil {
		t.Fatalf("ListForInbound(ws): %v", err)
	}
	if len(wsList) != 1 || wsList[0].Flow != "" {
		t.Errorf("bulk-attached non-flow inbound must not receive Vision flow, got %#v", wsList)
	}
}
