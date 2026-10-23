package job

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/runtime"
)

func TestNodeSyncDoesNotAdoptAnEmptySelection(t *testing.T) {
	initTestDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var obj any = []any{}
		switch r.URL.Path {
		case "/panel/api/inbounds/list":
			obj = []model.Inbound{{Id: 5, Tag: "existing", Enable: true, Port: 443, Protocol: model.VLESS, Settings: `{"clients":[]}`}}
		case "/panel/api/clients/onlinesByGuid", "/panel/api/clients/lastOnline":
			obj = map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{"success": true, "obj": obj}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	address, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(address.Port())
	if err != nil {
		t.Fatal(err)
	}
	node := &model.Node{Name: "onboarding", Scheme: "http", Address: address.Hostname(), Port: port, BasePath: "/", ApiToken: "token", Enable: true, AllowPrivateAddress: true, InboundSyncMode: "selected"}
	if err := database.GetDB().Create(node).Error; err != nil {
		t.Fatal(err)
	}
	manager := runtime.NewManager(runtime.LocalDeps{})
	job := &NodeTrafficSyncJob{}
	job.syncOne(manager, node, false)
	var saved model.Node
	if err := database.GetDB().First(&saved, node.Id).Error; err != nil {
		t.Fatal(err)
	}
	if saved.InboundsAdoptedAt != 0 {
		t.Fatalf("empty selection marked adopted at %d", saved.InboundsAdoptedAt)
	}
	node.InboundTags = []string{"existing"}
	if err := database.GetDB().Model(node).Update("inbound_tags", `["existing"]`).Error; err != nil {
		t.Fatal(err)
	}
	job.syncOne(manager, node, false)
	if err := database.GetDB().First(&saved, node.Id).Error; err != nil {
		t.Fatal(err)
	}
	if saved.InboundsAdoptedAt == 0 {
		t.Fatal("selected existing inbound was never adopted")
	}
	var count int64
	if err := database.GetDB().Model(&model.Inbound{}).Where("node_id = ?", node.Id).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("adopted inbound count=%d, want1", count)
	}
}
