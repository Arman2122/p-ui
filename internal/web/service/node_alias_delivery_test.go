package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/runtime"
)

func TestAdoptedNodeAliasPreservesPendingEditsAndCacheIdentity(t *testing.T) {
	for _, dirty := range []bool{false, true} {
		t.Run(map[bool]string{false: "clean adoption", true: "pending edit"}[dirty], func(t *testing.T) {
			setupConflictDB(t)
			var mu sync.Mutex
			var paths []string
			reportsCentral := false
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				mu.Lock()
				defer mu.Unlock()
				var obj any
				if req.URL.Path == "/panel/api/inbounds/list" {
					if reportsCentral {
						obj = []map[string]any{{"id": 42, "tag": "central", "listen": "", "port": 8443, "protocol": "vless"}, {"id": 77, "tag": "deployed"}}
					} else {
						obj = []map[string]any{{"id": 41, "tag": "deployed", "listen": "", "port": 8443, "protocol": "vless"}}
					}
				} else {
					paths = append(paths, req.URL.Path)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "obj": obj})
			}))
			t.Cleanup(srv.Close)
			node := reconcileTestNode(t, srv, "alias-delivery", "all", nil)
			node.Guid, node.ConfigDirty = "delivery-origin", dirty
			if err := database.GetDB().Model(node).Updates(map[string]any{"guid": node.Guid, "config_dirty": dirty}).Error; err != nil {
				t.Fatal(err)
			}
			seedInboundConflictNode(t, "central", "", 8443, model.VLESS, `{"network":"tcp"}`, `{"clients":[]}`, &node.Id)
			if err := database.GetDB().Model(&model.Inbound{}).Where("tag = ?", "central").Update("origin_node_guid", node.Guid).Error; err != nil {
				t.Fatal(err)
			}
			rt := runtime.NewRemote(node, nil)
			if err := (&InboundService{}).ReconcileNode(context.Background(), rt, node); err != nil {
				t.Fatal(err)
			}
			want := ""
			if dirty {
				want = "/panel/api/inbounds/update/41"
			}
			mu.Lock()
			got := strings.Join(paths, ",")
			paths = nil
			mu.Unlock()
			if got != want {
				t.Fatalf("adoption mutations = %q, want %q", got, want)
			}
			ib := loadInboundByTag(t, "central")
			for _, central := range []bool{false, true} {
				mu.Lock()
				reportsCentral = central
				mu.Unlock()
				if _, err := rt.ListRemoteTags(context.Background()); err != nil {
					t.Fatal(err)
				}
				if err := rt.UpdateInbound(context.Background(), ib, ib); err != nil {
					t.Fatal(err)
				}
			}
			mu.Lock()
			got = strings.Join(paths, ",")
			mu.Unlock()
			if got != "/panel/api/inbounds/update/41,/panel/api/inbounds/update/42" {
				t.Fatalf("refreshed alias delivery = %q", got)
			}
		})
	}
}

func TestNodeSnapshotAliasIsReadyForAnImmediateEdit(t *testing.T) {
	setupConflictDB(t)
	paths := make(chan string, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if req.URL.Path == "/panel/api/inbounds/list" {
			_, _ = w.Write([]byte(`{"success":true,"obj":[{"id":41,"tag":"deployed"}]}`))
			return
		}
		paths <- req.URL.Path
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	t.Cleanup(srv.Close)
	node := reconcileTestNode(t, srv, "snapshot-alias", "all", nil)
	node.Guid = "snapshot-origin"
	if err := database.GetDB().Model(node).Update("guid", node.Guid).Error; err != nil {
		t.Fatal(err)
	}
	seedInboundConflictNode(t, "central", "", 8443, model.VLESS, `{"network":"tcp"}`, `{"clients":[]}`, &node.Id)
	if err := database.GetDB().Model(&model.Inbound{}).Where("tag = ?", "central").Update("origin_node_guid", node.Guid).Error; err != nil {
		t.Fatal(err)
	}
	rt := runtime.NewRemote(node, nil)
	previous := runtime.GetManager()
	mgr := runtime.NewManager(runtime.LocalDeps{Cores: testCores(t)})
	mgr.SetRuntimeOverride(node.Id, rt)
	runtime.SetManager(mgr)
	t.Cleanup(func() { runtime.SetManager(previous) })
	snap := &runtime.TrafficSnapshot{Inbounds: []*model.Inbound{{Id: 41, Tag: "deployed", Enable: true, Port: 8443, Protocol: model.VLESS, Settings: `{"clients":[]}`, StreamSettings: `{"network":"tcp"}`}}}
	if _, err := (&InboundService{}).setRemoteTrafficLocked(node.Id, snap, false); err != nil {
		t.Fatal(err)
	}
	ib := loadInboundByTag(t, "central")
	if err := rt.UpdateInbound(context.Background(), ib, ib); err != nil {
		t.Fatal(err)
	}
	if got := <-paths; got != "/panel/api/inbounds/update/41" {
		t.Fatalf("immediate alias edit called %q, want existing inbound 41", got)
	}
}
