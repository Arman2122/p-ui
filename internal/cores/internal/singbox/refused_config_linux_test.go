//go:build linux

package singbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
)

/*
A refused config was written to the live path before `sing-box check` ran: the
running daemon kept serving from memory, so the inbound looked fine, and the
next crash or restart started on the poisoned file. The check reads a staged
copy, and only an accepted one becomes the file the daemon loads.
*/
func TestARefusedConfigNeverBecomesTheLiveFile(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := selfSigned(t, dir)
	refusing := filepath.Join(dir, "sing-box")
	if err := os.WriteFile(refusing, []byte("#!/bin/sh\ncase \"$1\" in check) echo 'FATAL[0000] decode config: bad'; exit 3;; esac\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	inst := core.Instance{
		ID: 3, Kind: KindTUIC, Tag: "tuic-3", Listen: "127.0.0.1", Port: 19443, Enable: true,
		Settings: fmt.Sprintf(`{"certFile":%q,"keyFile":%q}`, certFile, keyFile),
		Users:    []core.User{userN(KindTUIC, 1)},
	}
	c := New(Deps{Backend: newUnitLedger(), Binary: refusing, StateDir: dir, ApplyDelay: time.Hour})
	err := c.Reconcile(context.Background(), []core.Instance{inst})
	if err == nil || !strings.Contains(err.Error(), "config refused") {
		t.Fatalf("Reconcile = %v; want the daemon's refusal", err)
	}
	live := filepath.Join(dir, "3", "config.json")
	if _, err := os.Stat(live); !os.IsNotExist(err) {
		t.Fatalf("the refused config is the live file at %s (stat: %v); the next restart would load it", live, err)
	}
	if stray, _ := filepath.Glob(filepath.Join(dir, "3", "*.tmp")); len(stray) != 0 {
		t.Fatalf("the refused config was left staged: %v", stray)
	}
}
