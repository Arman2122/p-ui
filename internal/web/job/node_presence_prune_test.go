package job

import (
	"errors"
	"slices"
	"testing"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/runtime"
	"github.com/Arman2122/p-ui/internal/web/service"
)

func TestTrafficSyncDropsUnsyncedNodePresence(t *testing.T) {
	for _, state := range []string{"disabled", "offline", "deleted"} {
		t.Run(state, func(t *testing.T) {
			initTestDB(t)
			previous := runtime.GetManager()
			runtime.SetManager(runtime.NewManager(runtime.LocalDeps{}))
			t.Cleanup(func() { runtime.SetManager(previous) })
			node := &model.Node{Name: "retired", Enable: true, Status: "online", Address: "example.invalid", Port: 443}
			if err := database.GetDB().Create(node).Error; err != nil {
				t.Fatal(err)
			}
			inbounds := &service.InboundService{}
			inbounds.SetNodeOnlineTree(node.Id, map[string][]string{"parent": {"node-active"}, "child": {"descendant-active"}})
			t.Cleanup(func() { inbounds.ClearNodeOnlineClients(node.Id) })
			var err error
			switch state {
			case "disabled":
				err = database.GetDB().Model(node).Update("enable", false).Error
			case "offline":
				err = database.GetDB().Model(node).Update("status", "offline").Error
			case "deleted":
				err = database.GetDB().Delete(node).Error
			}
			if err != nil {
				t.Fatal(err)
			}
			NewNodeTrafficSyncJob().Run()
			if got := inbounds.GetOnlineClients(); slices.Contains(got, "node-active") || slices.Contains(got, "descendant-active") {
				t.Fatalf("presence after %s = %v; retired subtree must be absent", state, got)
			}
		})
	}
}

func TestFailedSnapshotMergeDropsNodePresence(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()
	failure := errors.New("forced snapshot read failure")
	const name = "test:failed-node-merge"
	if err := db.Callback().Query().Before("gorm:query").Register(name, func(tx *gorm.DB) { _ = tx.AddError(failure) }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Query().Remove(name) })
	inbounds := &service.InboundService{}
	inbounds.SetNodeOnlineTree(999, map[string][]string{"failed-node": {"stale-online"}})
	t.Cleanup(func() { inbounds.ClearNodeOnlineClients(999) })
	if _, err := inbounds.SetRemoteTraffic(999, &runtime.TrafficSnapshot{}, false); !errors.Is(err, failure) {
		t.Fatalf("merge error = %v, want forced failure", err)
	}
	if got := inbounds.GetOnlineClients(); slices.Contains(got, "stale-online") {
		t.Fatalf("failed snapshot retained online client: %v", got)
	}
}
