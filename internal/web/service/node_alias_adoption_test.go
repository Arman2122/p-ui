package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/runtime"
)

func TestReconcileNode_AdoptsCompatibleOriginInboundWithoutRemoteMutation(t *testing.T) {
	setupConflictDB(t)

	var mu sync.Mutex
	mutations := 0
	writeOK := func(w http.ResponseWriter, obj any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "msg": "", "obj": obj})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/panel/api/inbounds/list", func(w http.ResponseWriter, _ *http.Request) {
		writeOK(w, []map[string]any{{"id": 41, "tag": "already-deployed", "listen": "", "port": 8443, "protocol": "vless"}})
	})
	mux.HandleFunc("/panel/api/inbounds/", func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		mutations++
		mu.Unlock()
		writeOK(w, nil)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	node := reconcileTestNode(t, ts, "adopt-node", "all", nil)
	node.Guid = "origin-guid"
	if err := database.GetDB().Model(node).Update("guid", node.Guid).Error; err != nil {
		t.Fatalf("update node guid: %v", err)
	}
	seedInboundConflictNode(t, "desired-name", "", 8443, model.VLESS, `{"network":"tcp"}`, `{"clients":[]}`, &node.Id)
	if err := database.GetDB().Model(&model.Inbound{}).Where("tag = ?", "desired-name").Update("origin_node_guid", node.Guid).Error; err != nil {
		t.Fatalf("set origin guid: %v", err)
	}

	svc := InboundService{}
	rt := runtime.NewRemote(node, nil)
	if err := svc.ReconcileNode(context.Background(), rt, node); err != nil {
		t.Fatalf("first ReconcileNode: %v", err)
	}
	if err := svc.ReconcileNode(context.Background(), rt, node); err != nil {
		t.Fatalf("second ReconcileNode: %v", err)
	}
	mu.Lock()
	got := mutations
	mu.Unlock()
	if got != 0 {
		t.Fatalf("remote mutations = %d, want 0 while adopting compatible deployed inbound", got)
	}
}

func TestReconcileNode_AmbiguousCompatibleInboundsAreNotSwept(t *testing.T) {
	setupConflictDB(t)

	ts, deletedIDs := fakeNodePanel(t, map[string]int{"alias-a": 51, "alias-b": 52})
	node := reconcileTestNode(t, ts, "ambiguous-node", "all", nil)
	node.Guid = "origin-guid"
	if err := database.GetDB().Model(node).Update("guid", node.Guid).Error; err != nil {
		t.Fatalf("update node guid: %v", err)
	}
	seedInboundConflictNode(t, "desired-name", "", 0, model.Protocol(""), `{}`, `{"clients":[]}`, &node.Id)
	if err := database.GetDB().Model(&model.Inbound{}).Where("tag = ?", "desired-name").Update("origin_node_guid", node.Guid).Error; err != nil {
		t.Fatalf("set origin guid: %v", err)
	}

	err := (&InboundService{}).ReconcileNode(context.Background(), runtime.NewRemote(node, nil), node)
	if err == nil || !strings.Contains(err.Error(), "ambiguous compatible remote inbounds") {
		t.Fatalf("ReconcileNode error = %v, want ambiguity error", err)
	}
	if got := deletedIDs(); len(got) != 0 {
		t.Fatalf("deleted ambiguous candidates = %v, want none", got)
	}
}

func TestReconcileNode_IncompatiblePortOccupantRemainsLoud(t *testing.T) {
	setupConflictDB(t)

	writeOK := func(w http.ResponseWriter, obj any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "msg": "", "obj": obj})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/panel/api/inbounds/list", func(w http.ResponseWriter, _ *http.Request) {
		writeOK(w, []map[string]any{{"id": 42, "tag": "port-owner", "listen": "", "port": 9443, "protocol": "trojan"}})
	})
	mux.HandleFunc("/panel/api/inbounds/add", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "msg": "port already occupied", "obj": nil})
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	node := reconcileTestNode(t, ts, "drift-node", "all", nil)
	node.Guid = "origin-guid"
	seedInboundConflictNode(t, "desired-name", "", 9443, model.VLESS, `{"network":"tcp"}`, `{"clients":[]}`, &node.Id)
	if err := database.GetDB().Model(&model.Inbound{}).Where("tag = ?", "desired-name").Update("origin_node_guid", node.Guid).Error; err != nil {
		t.Fatalf("set origin guid: %v", err)
	}

	err := (&InboundService{}).ReconcileNode(context.Background(), runtime.NewRemote(node, nil), node)
	if err == nil || !strings.Contains(err.Error(), "port already occupied") {
		t.Fatalf("ReconcileNode error = %v, want loud incompatible-port error", err)
	}
}

// A panel-created node inbound is stored as "n<id>-tag" and pushed to the node
// with the prefix stripped, so the sweep's selected set must match both forms.
func TestReconcileNode_SelectedModeSweepsPrefixedSelectedTag(t *testing.T) {
	setupConflictDB(t)

	ts, deletedIDs := fakeNodePanel(t, map[string]int{
		"keep":          1,
		"selected-gone": 2,
		"unmanaged":     3,
	})
	node := reconcileTestNode(t, ts, "sel-prefix-node", "selected", nil)
	prefix := fmt.Sprintf("n%d-", node.Id)
	node.InboundTags = []string{prefix + "keep", prefix + "selected-gone"}
	seedInboundConflictNode(t, prefix+"keep", "", 443, model.VLESS, `{"network":"tcp"}`, `{"clients":[]}`, &node.Id)

	svc := InboundService{}
	if err := svc.ReconcileNode(context.Background(), runtime.NewRemote(node, nil), node); err != nil {
		t.Fatalf("ReconcileNode: %v", err)
	}

	got := deletedIDs()
	if len(got) != 1 || got[0] != 2 {
		t.Fatalf("deleted remote ids = %v, want [2] (prefixed selected tag must be swept, unmanaged 3 must survive)", got)
	}
}

func TestSetRemoteTraffic_AdoptsCompatibleOriginAliasWithoutDuplicate(t *testing.T) {
	setupConflictDB(t)
	db := database.GetDB()

	const nodeID = 1
	if err := db.Create(&model.Node{Id: nodeID, Name: "node", Address: "10.0.0.2", Port: 2053, ApiToken: "t", Guid: "node-guid"}).Error; err != nil {
		t.Fatalf("create node: %v", err)
	}
	id := nodeID
	central := &model.Inbound{UserId: 1, NodeID: &id, OriginNodeGuid: "node-guid", Tag: "desired-name", Enable: true, Port: 8443, Protocol: model.VLESS, Settings: `{"clients":[]}`}
	if err := db.Create(central).Error; err != nil {
		t.Fatalf("create central inbound: %v", err)
	}

	snap := &runtime.TrafficSnapshot{Inbounds: []*model.Inbound{{
		Tag: "already-deployed", Enable: true, Port: 8443, Protocol: model.VLESS,
		Settings: `{"clients":[]}`, Up: 11, Down: 22,
	}}}
	if _, err := (&InboundService{}).setRemoteTrafficLocked(nodeID, snap, false); err != nil {
		t.Fatalf("setRemoteTrafficLocked: %v", err)
	}

	var rows []model.Inbound
	if err := db.Where("node_id = ?", nodeID).Find(&rows).Error; err != nil {
		t.Fatalf("list node inbounds: %v", err)
	}
	if len(rows) != 1 || rows[0].Id != central.Id || rows[0].Tag != "desired-name" {
		t.Fatalf("alias adoption rows = %#v, want original central inbound only", rows)
	}
	if rows[0].Up != 11 || rows[0].Down != 22 {
		t.Fatalf("alias traffic = %d/%d, want 11/22", rows[0].Up, rows[0].Down)
	}
}
