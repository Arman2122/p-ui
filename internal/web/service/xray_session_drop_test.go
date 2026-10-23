package service

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/xray"
)

func sessionDropCore(t *testing.T) (*XrayService, *xray.Process) {
	t.Helper()
	setupSettingTestDB(t)
	previous, result := xrayState().Snapshot()
	requested, completed := sessionDropRequested.Load(), sessionDropCompleted.Load()
	manuallyStopped, restart := isManuallyStopped.Load(), isNeedXrayRestart.Load()
	sessionDropRequested.Store(0)
	sessionDropCompleted.Store(0)
	isManuallyStopped.Store(false)
	isNeedXrayRestart.Store(false)
	xrayState().Replace(nil)
	t.Cleanup(func() {
		if process := currentXrayProcess(); process != nil {
			_ = process.Stop()
		}
		egressKicks.Wait()
		xrayState().Replace(previous)
		xrayState().StoreResult(previous, result)
		sessionDropRequested.Store(requested)
		sessionDropCompleted.Store(completed)
		isManuallyStopped.Store(manuallyStopped)
		isNeedXrayRestart.Store(restart)
	})
	dir := t.TempDir()
	t.Setenv("PUI_BIN_FOLDER", dir)
	t.Setenv("PUI_LOG_FOLDER", t.TempDir())
	script := `#!/bin/sh
if [ "$1" = "-version" ]; then echo 'Xray test'; exit 0; fi
if [ "$1" = "-test" ]; then
 if [ -n "$PUI_TEST_DROP_GATE" ]; then
  touch "$PUI_TEST_DROP_GATE/ready"
  while [ ! -f "$PUI_TEST_DROP_GATE/release" ]; do sleep 0.01; done
 fi
 exit 0
fi
exec sleep 600
`
	if err := os.WriteFile(filepath.Join(dir, xray.GetBinaryName()), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	_ = listener.Close()
	settings := &SettingService{}
	raw, err := settings.GetXrayConfigTemplate()
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(raw), &config); err != nil {
		t.Fatal(err)
	}
	for _, row := range config["inbounds"].([]any) {
		inbound := row.(map[string]any)
		if inbound["tag"] == "api" {
			inbound["port"] = port
		}
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := settings.saveSetting("xrayTemplateConfig", string(encoded)); err != nil {
		t.Fatal(err)
	}
	svc := &XrayService{}
	if err := svc.RestartXray(true); err != nil {
		t.Fatal(err)
	}
	return svc, currentXrayProcess()
}

func TestPendingSessionDropSurvivesCaughtUpConfig(t *testing.T) {
	svc, old := sessionDropCore(t)
	queueXraySessionDrop()
	svc.IsNeedRestartAndSetFalse()
	svc.ApplyPendingRestart()
	if currentXrayProcess() == old || old.IsRunning() {
		t.Fatal("matching config swallowed the pending session revocation")
	}
	if _, pending := pendingXraySessionDrop(); pending {
		t.Fatal("successful replacement did not acknowledge session revocation")
	}
}

func TestSessionDropRestartKeepsNewerRevision(t *testing.T) {
	svc, old := sessionDropCore(t)
	gate := t.TempDir()
	t.Setenv("PUI_TEST_DROP_GATE", gate)
	queueXraySessionDrop()
	captured := sessionDropRequested.Load()
	done := make(chan struct{})
	t.Cleanup(func() {
		if err := os.WriteFile(filepath.Join(gate, "release"), nil, 0o600); err != nil {
			t.Errorf("release restart gate: %v", err)
		}
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("restart did not leave the released gate")
		}
	})
	go func() { svc.ApplyPendingRestart(); close(done) }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(gate, "ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("restart did not reach preflight gate")
		}
		time.Sleep(10 * time.Millisecond)
	}
	queueXraySessionDrop()
	if err := os.WriteFile(filepath.Join(gate, "release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("restart remained blocked")
	}
	if currentXrayProcess() == old || sessionDropCompleted.Load() != captured {
		t.Fatalf("restart acknowledged revision %d, want captured %d", sessionDropCompleted.Load(), captured)
	}
	if _, pending := pendingXraySessionDrop(); !pending {
		t.Fatal("older restart consumed a newer revocation")
	}
	svc.ApplyPendingRestart()
	if _, pending := pendingXraySessionDrop(); pending {
		t.Fatal("second replacement did not consume remaining revocation")
	}
}

func TestSessionDropHonorsManualStop(t *testing.T) {
	svc, old := sessionDropCore(t)
	queueXraySessionDrop()
	if err := svc.StopXray(); err != nil {
		t.Fatal(err)
	}
	if _, pending := pendingXraySessionDrop(); pending {
		t.Fatal("stopped process retained already-ended session debt")
	}
	queueXraySessionDrop()
	svc.ApplyPendingRestart()
	if currentXrayProcess() != old || old.IsRunning() || !isManuallyStopped.Load() {
		t.Fatal("session revocation revived a manually stopped core")
	}
	if _, pending := pendingXraySessionDrop(); !pending {
		t.Fatal("non-restart consumed a pending revocation")
	}
}
