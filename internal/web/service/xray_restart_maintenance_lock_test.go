package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/runtime"
)

type applyLockRemovalRuntime struct {
	fakeNodeRuntime
	done    chan struct{}
	blocked bool
}

func (r *applyLockRemovalRuntime) DelInbound(ctx context.Context, inbound *model.Inbound) error {
	go func() {
		xrayState().LockApply()
		defer xrayState().UnlockApply()
		_ = r.fakeNodeRuntime.DelInbound(ctx, inbound)
		close(r.done)
	}()
	select {
	case <-r.done:
		return nil
	case <-time.After(3 * time.Second):
		r.blocked = true
		return errors.New("runtime removal blocked by the restart apply lock")
	}
}

func TestRestartXrayLeavesApplyLockFreeForExpiryMaintenance(t *testing.T) {
	svc, _ := sessionDropCore(t)
	previous := runtime.GetManager()
	manager := runtime.NewManager(runtime.LocalDeps{Cores: testCores(t)})
	removal := &applyLockRemovalRuntime{done: make(chan struct{})}
	manager.SetLocalRuntimeOverride(removal)
	runtime.SetManager(manager)
	t.Cleanup(func() { runtime.SetManager(previous) })
	seedInboundConflict(t, "expired-during-restart", "", 47444, model.VLESS, "", `{"clients":[],"decryption":"none"}`)
	inbound := loadInboundByTag(t, "expired-during-restart")
	if err := database.GetDB().Model(inbound).Update("expiry_time", time.Now().Add(-time.Minute).UnixMilli()).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.RestartXray(false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-removal.done:
	case <-time.After(3 * time.Second):
		t.Fatal("runtime drop remained blocked after restart returned")
	}
	if removal.blocked {
		t.Fatal("restart held the process apply lock while expiry maintenance needed that same lock")
	}
	if removal.delInbound.Load() != 1 {
		t.Fatalf("runtime deletion calls=%d, want 1", removal.delInbound.Load())
	}
	if got := loadInboundByTag(t, "expired-during-restart"); got.Enable {
		t.Fatal("real expiry maintenance did not disable the inbound")
	}
}
