package service

import (
	"context"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/runtime"
)

/*
A row staged as disabled must be stored disabled.

GORM omits a zero-valued field from the INSERT when its column carries a
default, so the database supplies the default instead. Every model here declares
a boolean defaulting to true, so "create it switched off" wrote a row switched
ON — measured, and reachable on four of the five.

The consequences are not uniform, so each is driven through the path an operator
actually uses rather than through a raw db.Create:

  - a CLIENT staged as disabled was stored enabled, so it was not stripped from
    the runtime push and could connect immediately;
  - a NODE staged as off was heartbeated and had inbounds synced to it;
  - a SUBSCRIPTION staged as off was fetched and its outbounds merged;
  - a ROUTING RULE staged as off started routing traffic on save.

Each case fails without Select("*") on its own create.
*/

func TestAClientStagedAsDisabledIsStoredDisabled(t *testing.T) {
	setupConflictDB(t)
	mgr := runtime.NewManager(runtime.LocalDeps{Cores: testCores(t)})
	mgr.SetLocalRuntimeOverride(&fakeNodeRuntime{})
	runtime.SetManager(mgr)
	t.Cleanup(func() { runtime.SetManager(nil) })

	inboundSvc := &InboundService{}
	created, _, _, err := inboundSvc.AddInbound(wgkernelInbound(46420, "10.45.0.1/24"))
	if err != nil {
		t.Fatalf("AddInbound: %v", err)
	}

	add := &model.Inbound{Id: created.Id, Protocol: model.WGKernel, Settings: clientsSettings(t, []model.Client{
		{Email: "staged@example.com", Enable: false},
	})}
	if _, err := (&ClientService{}).AddInboundClient(inboundSvc, add); err != nil {
		t.Fatalf("AddInboundClient: %v", err)
	}

	var record model.ClientRecord
	if err := database.GetDB().Where("email = ?", "staged@example.com").First(&record).Error; err != nil {
		t.Fatalf("read the client back: %v", err)
	}
	if record.Enable {
		t.Fatal("a client the operator staged as disabled is stored enabled, so it is pushed to the core and can connect")
	}
}

func TestANodeStagedAsOffIsStoredOff(t *testing.T) {
	initTestDB(t)

	node := &model.Node{
		Name: "staged", Address: "node.example.com", Port: 2053,
		ApiToken: "token", Enable: false,
	}
	if err := (&NodeService{}).Create(node); err != nil {
		t.Fatalf("Create: %v", err)
	}

	var back model.Node
	if err := database.GetDB().First(&back, node.Id).Error; err != nil {
		t.Fatalf("read the node back: %v", err)
	}
	if back.Enable {
		t.Fatal("a node the operator staged as off is stored on, so the panel heartbeats and syncs inbounds to it")
	}
}

func TestASubscriptionStagedAsOffIsStoredOff(t *testing.T) {
	initTestDB(t)

	sub, err := (&OutboundSubscriptionService{}).Create(
		"staged", "https://example.com/sub", "", false, 600, false, false, false)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	var back model.OutboundSubscription
	if err := database.GetDB().First(&back, sub.Id).Error; err != nil {
		t.Fatalf("read the subscription back: %v", err)
	}
	if back.Enabled {
		t.Fatal("a subscription the operator staged as off is stored on, so it is fetched and its outbounds merged")
	}
}

func TestARoutingRuleStagedAsOffIsStoredOff(t *testing.T) {
	initTestDB(t)
	withFakeEgressKernel(t)
	installCores(t, newProxyCore(&declaredCore{id: "proxy", kind: "vless"}))
	inbound := seedPolicyInbound(t, "vless", "vless-staged", 45721, "client")

	created, err := (&RoutingService{}).Add(context.Background(), &model.RoutingRule{
		Enable: false, Remark: "staged off",
		IngressScope: model.RoutingScopeSelected,
		IngressIds:   ingressIdsJSON(t, inbound.Id),
		DestKind:     model.RoutingDestBlock,
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	var back model.RoutingRule
	if err := database.GetDB().First(&back, created.Id).Error; err != nil {
		t.Fatalf("read the rule back: %v", err)
	}
	if back.Enable {
		t.Fatal("a rule the operator staged as off is stored on and starts routing traffic immediately")
	}
}
