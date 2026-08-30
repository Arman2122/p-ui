package service

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/xray"
)

func TestRestartXrayRespectsManualStop(t *testing.T) {
	setupSettingTestDB(t)
	if err := (&SettingService{}).saveSetting("xrayTemplateConfig", "{ not valid json"); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	t.Cleanup(func() { isManuallyStopped.Store(false) })

	isManuallyStopped.Store(true)
	_ = (&XrayService{}).RestartXray(false)

	if !isManuallyStopped.Load() {
		t.Fatal("a non-forced restart cleared a deliberate manual stop and would revive xray")
	}
}

func TestApplyPendingRestartReArmsFlagOnFailure(t *testing.T) {
	setupSettingTestDB(t)
	if err := (&SettingService{}).saveSetting("xrayTemplateConfig", "{ not valid json"); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	t.Cleanup(func() {
		isManuallyStopped.Store(false)
		isNeedXrayRestart.Store(false)
	})
	isManuallyStopped.Store(false)

	svc := &XrayService{}
	svc.SetToNeedRestart()
	svc.ApplyPendingRestart()

	if !isNeedXrayRestart.Load() {
		t.Fatal("a failed restart must re-arm the need-restart flag so the pending config change is retried")
	}
}

/*
A cold start must not report success for a config the core refuses.

The pre-flight added by F004 lives INSIDE the `process != nil && IsRunning()`
branch, so it guards a restart and nothing else. When xray is not running --
panel boot, the crash-recovery job, `p-ui restart`, the first start after an
install -- RestartXray went straight to NewProcess and Start.

Start returns nil once cmd.Start() succeeds. The core then reads the config,
refuses it, and exits; the failure is logged from waitForCommand on another
goroutine, while RestartXray has already returned success. The operator's
Restart button says it worked, the settings page saves, and the panel is simply
serving nothing.

The stand-in core exits 23 with the line the real core prints, which is what
coreRefusal parses. The assertion is that exact substring, never `err != nil`:
GetXrayConfig or a busy API port could also return an error, and either would
pass a nil-check while proving nothing about the pre-flight. F007.
*/
func TestRestartXrayRefusesAConfigTheCoreRejectsWhenNothingIsRunning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in core is a shell script")
	}
	setupSettingTestDB(t)

	// A template that PARSES, unlike the two tests above -- the cold branch is
	// only reached when GetXrayConfig succeeds.
	dir := t.TempDir()
	t.Setenv("PUI_BIN_FOLDER", dir)
	writeStandInCoreScript(t, filepath.Join(dir, xray.GetBinaryName()), 23,
		"Failed to start: main: failed to load config")

	t.Cleanup(func() {
		isManuallyStopped.Store(false)
		isNeedXrayRestart.Store(false)
		if p := currentXrayProcess(); p != nil {
			_ = p.Stop()
		}
		xrayState().Replace(nil)
	})

	err := (&XrayService{}).RestartXray(true)
	if err == nil {
		t.Fatal("RestartXray returned nil after launching a core that cannot load this config; the operator is told the restart worked while the panel serves nothing")
	}
	if !strings.Contains(err.Error(), "main: failed to load config") {
		t.Fatalf("the error did not come from the core's own refusal, so the pre-flight did not run: %v", err)
	}
}

// The other half: the new branch must fail open, or it becomes a new reason a
// start cannot happen.
func TestRestartXrayStartsWhenTheCoreAcceptsTheConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stand-in core is a shell script")
	}
	setupSettingTestDB(t)

	dir := t.TempDir()
	t.Setenv("PUI_BIN_FOLDER", dir)
	writeStandInCoreScript(t, filepath.Join(dir, xray.GetBinaryName()), 0, "")

	t.Cleanup(func() {
		isManuallyStopped.Store(false)
		isNeedXrayRestart.Store(false)
		if p := currentXrayProcess(); p != nil {
			_ = p.Stop()
		}
		xrayState().Replace(nil)
	})

	// Asserts the property, not a clean start: this box may already have an
	// xray holding the api port, which is a different failure entirely.
	if err := (&XrayService{}).RestartXray(true); err != nil &&
		strings.Contains(err.Error(), "rejected by the core") {
		t.Fatalf("the pre-flight refused a config the core accepts, so it has become a new reason a start cannot happen: %v", err)
	}
}

// writeStandInCoreScript is internal/xray's writeStandInCore; that one is in
// package xray and this test cannot reach across.
func writeStandInCoreScript(t *testing.T, path string, exitCode int, message string) {
	t.Helper()
	script := "#!/bin/sh\n"
	if message != "" {
		script += "echo \"" + message + "\"\n"
	}
	script += "exit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write stand-in core at %s: %v", path, err)
	}
}
