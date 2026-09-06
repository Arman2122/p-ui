package service

import (
	"reflect"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
The desired set a core reconciles on and the render the hot path applies are
one state: a client depletion or the operator disabled is on neither, an
inbound emptied that way stays desired carrying nobody (which is how its core
stops it), and a node's inbound is on neither.
*/
func TestTheReconcileSetAndTheHotPathRenderAgree(t *testing.T) {
	setupConflictDB(t)
	svc := &InboundService{}

	seedInboundConflict(t, "mt-desired", "", 46001, model.MTProto,
		"",
		`{"clients":[`+
			`{"email":"alice","secret":"`+mtprotoTestSecretA+`","enable":true},`+
			`{"email":"bob","secret":"`+mtprotoTestSecretB+`","enable":true},`+
			`{"email":"carol","secret":"`+mtprotoTestSecretC+`","enable":false}]}`)
	served := loadInboundByTag(t, "mt-desired")
	seedClientTraffic(t, served.Id, "alice", true)
	seedClientTraffic(t, served.Id, "bob", false)
	seedClientTraffic(t, served.Id, "carol", true)

	seedInboundConflict(t, "mt-all-depleted", "", 46002, model.MTProto,
		"",
		`{"clients":[{"email":"dave","secret":"`+mtprotoTestSecretA+`","enable":true}]}`)
	emptied := loadInboundByTag(t, "mt-all-depleted")
	seedClientTraffic(t, emptied.Id, "dave", false)

	nodeID := 5
	seedInboundConflictNode(t, "mt-node-owned", "", 46003, model.MTProto,
		"",
		`{"clients":[{"email":"erin","secret":"`+mtprotoTestSecretB+`","enable":true}]}`,
		&nodeID)
	nodeOwned := loadInboundByTag(t, "mt-node-owned")

	instances, err := svc.DesiredInstances([]core.Kind{core.Kind(model.MTProto)})
	if err != nil {
		t.Fatalf("DesiredInstances: %v", err)
	}
	users := map[int][]string{}
	for _, inst := range instances {
		users[inst.ID] = instanceEmails(inst)
	}

	t.Run("reconcileSet", func(t *testing.T) {
		if got, want := users[served.Id], []string{"alice"}; !reflect.DeepEqual(got, want) {
			t.Errorf("served users: got %v, want %v", got, want)
		}
		if got, ok := users[emptied.Id]; !ok || len(got) != 0 {
			t.Errorf("emptied inbound: desired %v carrying %v, want desired carrying nobody", ok, got)
		}
		if _, ok := users[nodeOwned.Id]; ok {
			t.Errorf("a node's inbound is in the local desired set")
		}
	})

	t.Run("hotPathRender", func(t *testing.T) {
		cfg, err := svc.RenderForRuntime(served)
		if err != nil {
			t.Fatalf("RenderForRuntime: %v", err)
		}
		if cfg == nil {
			t.Fatal("RenderForRuntime returned nil for a served local inbound")
		}
		if got, want := settingsEmails(t, string(cfg.Settings)), users[served.Id]; !reflect.DeepEqual(got, want) {
			t.Errorf("hot path renders %v, the reconcile set carries %v — a disagreement is a needless restart of the core", got, want)
		}
		if cfg, err := svc.RenderForRuntime(nodeOwned); err != nil || cfg != nil {
			t.Errorf("RenderForRuntime(node's inbound) = %v, %v; want nil, nil", cfg, err)
		}
	})
}
