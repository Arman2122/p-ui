package xray

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

/*
The pre-flight must run the binary it is handed, not the installed one.

Before this, it could only ask GetBinaryPath(), so the version switcher tested a
downloaded Xray only AFTER renaming it over the one that works. A binary that
verifies but cannot load this panel's config was then refused with the unusable
binary already in place, and every later restart refused too. Ledger L2.
*/
func TestPreflightRunsTheBinaryItIsGiven(t *testing.T) {
	requirePosixHost(t)

	dir := t.TempDir()
	t.Setenv("PUI_BIN_FOLDER", dir)

	writeStandInCore(t, filepath.Join(dir, GetBinaryName()), 0, "")
	staged := filepath.Join(dir, "staged-xray")
	writeStandInCore(t, staged, 23, "Failed to start: the staged binary refuses this config")

	cfg := loadableConfig(t)

	if err := TestConfig(cfg); err != nil {
		t.Fatalf("the installed stand-in accepts, so TestConfig must pass: %v", err)
	}

	err := TestConfigWith(staged, cfg)
	if err == nil {
		t.Fatal("TestConfigWith ran the installed binary instead of the staged one: a bad download is then only discovered after it has replaced a good one")
	}
	if !strings.Contains(err.Error(), "the staged binary refuses this config") {
		t.Fatalf("the refusal must come from the staged binary, got: %v", err)
	}
}

// writeStandInCore puts a script where an xray binary would be. It prints what
// the real core prints on refusal, because coreRefusal parses that line.
func writeStandInCore(t *testing.T, path string, exitCode int, message string) {
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
