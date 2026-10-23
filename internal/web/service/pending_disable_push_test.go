package service

import (
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/runtime"
)

/*
The node push happens after the writer has released, not inside it.

runSerializedTx is the single goroutine every traffic poll and every client
mutation queues on, and its own contract asks for network I/O to stay out. The
deferred disable ran the node push inside it anyway: a node that had gone away
held that writer for the whole remote timeout, and the @every 5s traffic poll,
every quota decision and every admin edit on the panel waited behind it.

Proven by asking for another serialized transaction from inside the push. If the
push still runs inside the writer, that request can never be served and the wait
below times out -- which is exactly the stall the operator sees, in miniature.
F392.
*/
func TestTheNodePushIsMadeOutsideTheTrafficWriter(t *testing.T) {
	setupConflictDB(t)
	db := database.GetDB()

	const email = "depleted@node"
	seedInboundConflict(t, "node-push", "", 46411, model.VLESS, "",
		`{"clients":[{"id":"11111111-2222-4333-8444-000000046411","email":"`+email+`","enable":true}],"decryption":"none"}`)
	ib := loadInboundByTag(t, "node-push")
	record := &model.ClientRecord{Email: email}
	if err := db.Create(record).Error; err != nil {
		t.Fatalf("seed the client record: %v", err)
	}
	// enable carries gorm default:true, so a false on Create is written as true.
	if err := db.Model(record).Update("enable", false).Error; err != nil {
		t.Fatalf("deplete the client: %v", err)
	}

	// The traffic writer must actually be running: with none, runSerializedTx
	// executes inline and the property under test cannot be observed at all.
	StartTrafficWriter()
	t.Cleanup(StopTrafficWriter)

	writerFree := make(chan error, 1)
	fake := &fakeNodeRuntime{}
	fake.onUpdateInbound = func() {
		go func() {
			writerFree <- runSerializedTx(func(*gorm.DB) error { return nil })
		}()
		select {
		case err := <-writerFree:
			if err != nil {
				t.Errorf("the second serialized transaction failed: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("the traffic writer is still held while the node push waits; every poll and every client mutation on this panel is queued behind a remote timeout")
		}
	}

	mgr := runtime.NewManager(runtime.LocalDeps{Cores: testCores(t)})
	mgr.SetLocalRuntimeOverride(fake)
	runtime.SetManager(mgr)
	t.Cleanup(func() { runtime.SetManager(nil) })

	(&InboundService{}).applyPendingClientDisables(pendingTrafficApply{
		remote: map[int]map[string]struct{}{ib.Id: {email: {}}},
	})

	if fake.updateInbound.Load() != 1 {
		t.Fatalf("the node was pushed %d times, want 1; this test proves nothing", fake.updateInbound.Load())
	}
}
