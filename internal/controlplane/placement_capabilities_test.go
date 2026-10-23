package controlplane

import (
	"bytes"
	"errors"
	"math"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestPlacementCapabilitiesReconnectAndChangePostgres(t *testing.T) {
	store, db, nodes, _, _, _ := setupLedger(t)
	node := nodes[0]
	hello := journalHello(t, store, node)
	hello.Capabilities = []*nodev1.CoreCapability{
		{CoreId: "xray", Version: "26.7.28", Protocols: []string{"vless", "trojan"}, ClientRateEnforcement: true, ClientQuotaEnforcement: true, ActivationAdmission: true},
		{CoreId: "ssh", Version: "1", Protocols: []string{"ssh"}},
	}
	policy := model.FleetNodePlacementPolicy{NodeID: node.NodeID, InstallationID: node.InstallationID, Revision: 7, Enabled: true, Labels: `{"region":"approved"}`, InboundIDs: `[11]`, Digest: bytes.Repeat([]byte{1}, 32), ActorID: 9}
	planID := 1
	rule := model.FleetPlacementRule{ID: uuid.NewString(), Revision: 3, Scope: "plan", PlanID: &planID, Enabled: true, NodeIDs: `[]`, Labels: `{"region":"approved"}`, InboundIDs: `[11]`, Digest: bytes.Repeat([]byte{2}, 32), ActorID: 9}
	if err := db.Create(&policy).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&rule).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := store.BeginSession(t.Context(), node, hello); err != nil {
		t.Fatal(err)
	}
	var first model.FleetNodeCapabilitySnapshot
	if err := db.First(&first, "node_id = ?", node.NodeID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.FleetPlacementFanout{}).Where("scope = 'node'").Updates(map[string]any{"pending": false, "cursor": `{"client":"preserved"}`}).Error; err != nil {
		t.Fatal(err)
	}
	hello.Capabilities[0], hello.Capabilities[1] = hello.Capabilities[1], hello.Capabilities[0]
	hello.Capabilities[1].Protocols = []string{"trojan", "vless"}
	secondGeneration, err := store.BeginSession(t.Context(), node, hello)
	if err != nil {
		t.Fatal(err)
	}
	var second model.FleetNodeCapabilitySnapshot
	var work model.FleetPlacementFanout
	if err := db.First(&second, "node_id = ?", node.NodeID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&work, "scope = 'node' AND scope_id = ?", node.NodeID+"/"+node.InstallationID).Error; err != nil {
		t.Fatal(err)
	}
	if second.Revision != first.Revision || !bytes.Equal(second.Digest, first.Digest) || second.SessionGeneration != int64(secondGeneration) || second.SessionGeneration <= first.SessionGeneration || work.Pending || work.Revision != 1 {
		t.Fatalf("identical reconnect changed placement semantics: first=%+v second=%+v work=%+v", first, second, work)
	}
	hello.Capabilities[1].ClientRateEnforcement = false
	if _, err := store.BeginSession(t.Context(), node, hello); err != nil {
		t.Fatal(err)
	}
	var changed model.FleetNodeCapabilitySnapshot
	if err := db.First(&changed, "node_id = ?", node.NodeID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&work, "scope = 'node' AND scope_id = ?", node.NodeID+"/"+node.InstallationID).Error; err != nil {
		t.Fatal(err)
	}
	if changed.Revision != first.Revision+1 || bytes.Equal(changed.Digest, first.Digest) || !work.Pending || work.Revision != 2 || work.Cursor != "{}" {
		t.Fatalf("capability change did not enqueue exact installation: snapshot=%+v work=%+v", changed, work)
	}
	var actualPolicy model.FleetNodePlacementPolicy
	var actualRule model.FleetPlacementRule
	if err := db.First(&actualPolicy, "node_id = ?", policy.NodeID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&actualRule, "id = ?", rule.ID).Error; err != nil {
		t.Fatal(err)
	}
	if actualPolicy.Revision != policy.Revision || actualPolicy.ActorID != policy.ActorID || actualRule.Revision != rule.Revision || actualRule.ActorID != rule.ActorID || !bytes.Equal(actualPolicy.Digest, policy.Digest) || !bytes.Equal(actualRule.Digest, rule.Digest) {
		t.Fatal("node capabilities mutated master placement policy")
	}
}

func TestPlacementCapabilitiesRejectMalformedAtomicallyPostgres(t *testing.T) {
	store, db, nodes, _, _, _ := setupLedger(t)
	hello := journalHello(t, store, nodes[0])
	if _, err := store.BeginSession(t.Context(), nodes[0], hello); err != nil {
		t.Fatalf("empty capability list must remain valid: %v", err)
	}
	var before model.FleetNode
	if err := db.First(&before, "id = ?", nodes[0].NodeID).Error; err != nil {
		t.Fatal(err)
	}
	valid := &nodev1.CoreCapability{CoreId: "xray", Version: "1", Protocols: []string{"vless"}}
	for name, input := range map[string][]*nodev1.CoreCapability{
		"nil": {nil}, "duplicate_core": {valid, valid}, "bad_core": {{CoreId: "../../xray", Version: "1"}},
		"duplicate_protocol": {{CoreId: "xray", Version: "1", Protocols: []string{"vless", "vless"}}},
		"bad_version":        {{CoreId: "xray", Version: "1\nsecret"}}, "too_many": make([]*nodev1.CoreCapability, 129),
	} {
		t.Run(name, func(t *testing.T) {
			request := proto.Clone(hello).(*nodev1.NodeHello)
			request.Capabilities = input
			if _, err := store.BeginSession(t.Context(), nodes[0], request); !errors.Is(err, ErrInvalid) {
				t.Fatalf("malformed capability accepted: %v", err)
			}
		})
	}
	var after model.FleetNode
	var snapshot model.FleetNodeCapabilitySnapshot
	var work model.FleetPlacementFanout
	if err := db.First(&after, "id = ?", nodes[0].NodeID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&snapshot, "node_id = ?", nodes[0].NodeID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&work, "scope = 'node'").Error; err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) || snapshot.Capabilities != "[]" || snapshot.Revision != 1 || work.Revision != 1 {
		t.Fatalf("invalid hello changed durable session or placement state: before=%+v after=%+v snapshot=%+v work=%+v", before, after, snapshot, work)
	}
}

func TestPlacementCapabilitiesFanoutFailureRollsBackSessionPostgres(t *testing.T) {
	store, db, nodes, _, _, _ := setupLedger(t)
	hello := journalHello(t, store, nodes[0])
	if _, err := store.BeginSession(t.Context(), nodes[0], hello); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.FleetPlacementFanout{}).Where("scope = 'node'").Update("revision", int64(math.MaxInt64)).Error; err != nil {
		t.Fatal(err)
	}
	hello.Capabilities = []*nodev1.CoreCapability{{CoreId: "xray", Version: "1"}}
	if _, err := store.BeginSession(t.Context(), nodes[0], hello); !errors.Is(err, ErrRevision) {
		t.Fatalf("overflowing fanout accepted: %v", err)
	}
	var node model.FleetNode
	var snapshot model.FleetNodeCapabilitySnapshot
	if err := db.First(&node, "id = ?", nodes[0].NodeID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&snapshot, "node_id = ?", nodes[0].NodeID).Error; err != nil {
		t.Fatal(err)
	}
	if node.AuthorityGeneration != 1 || snapshot.Revision != 1 || snapshot.Capabilities != "[]" {
		t.Fatalf("fanout failure leaked session/capability writes: node=%+v snapshot=%+v", node, snapshot)
	}
}
