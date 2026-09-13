//go:build linux

package singbox

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/supervisor"
)

// unitLedger is a backend that remembers what it was asked, and counts reloads.
type unitLedger struct {
	mu      sync.Mutex
	units   map[string]supervisor.Spec
	reloads int
}

func newUnitLedger() *unitLedger { return &unitLedger{units: map[string]supervisor.Spec{}} }

func (b *unitLedger) Name() string { return "ledger" }

func (b *unitLedger) Ensure(_ context.Context, spec supervisor.Spec) error {
	if err := spec.Validate(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.units[spec.Name] = spec
	return nil
}

func (b *unitLedger) Status(_ context.Context, name string) (supervisor.Status, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.units[name]; !ok {
		return supervisor.Status{}, supervisor.ErrNotRunning
	}
	return supervisor.Status{State: supervisor.StateRunning}, nil
}

func (b *unitLedger) Stop(_ context.Context, name string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.units, name)
	return nil
}

func (b *unitLedger) Reload(context.Context, string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reloads++
	return nil
}

func (b *unitLedger) Logs(context.Context, string, int) ([]string, error) { return nil, nil }

func (b *unitLedger) List(context.Context) ([]string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Sorted(maps.Keys(b.units)), nil
}

func (b *unitLedger) Close() error { return nil }

func (b *unitLedger) has(name string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.units[name]
	return ok
}

/*
A daemon that outlived the panel was never adopted: the unit was asked about
under inbound id 0, so the ports its file names were discarded, a fresh control
minted, and the "stale" branch reloaded every daemon on every panel restart —
dropping every connection the restart was supposed to leave alone.
*/
func TestADaemonThatOutlivedThePanelIsAdoptedWithoutAReload(t *testing.T) {
	dir := t.TempDir()
	certFile, keyFile := selfSigned(t, dir)
	inst := core.Instance{
		ID: 3, Kind: KindTUIC, Tag: "tuic-3", Listen: "127.0.0.1", Port: 19443, Enable: true,
		Settings: fmt.Sprintf(`{"certFile":%q,"keyFile":%q,"serverName":"localhost"}`, certFile, keyFile),
		Users: []core.User{{Email: "u@x", Enable: true, Credentials: map[string]any{
			core.CredUUID: "c0ffee00-0000-4000-8000-000000000001", core.CredPassword: "secret-password-1",
		}}},
	}
	prevStats, prevStarted := readUserStats, readStartedAt
	readUserStats = func(context.Context, int) (map[string]int64, error) { return map[string]int64{}, nil }
	readStartedAt = func(context.Context, int, string) (int64, error) { return 1, nil }
	t.Cleanup(func() { readUserStats, readStartedAt = prevStats, prevStarted })

	ledger := newUnitLedger()
	ctx := context.Background()
	first := New(Deps{Backend: ledger, Binary: "/bin/true", StateDir: dir, ApplyDelay: time.Hour})
	if err := first.Reconcile(ctx, []core.Instance{inst}); err != nil {
		t.Fatal(err)
	}
	held := first.instances[3].ctl

	// The panel restarts: a new core over the same state, the unit still up.
	second := New(Deps{Backend: ledger, Binary: "/bin/true", StateDir: dir, ApplyDelay: time.Hour})
	if err := second.Reconcile(ctx, []core.Instance{inst}); err != nil {
		t.Fatal(err)
	}
	if got := second.instances[3].ctl; got != held {
		t.Fatalf("adopted control %+v, want the file's %+v", got, held)
	}
	if ledger.reloads != 0 {
		t.Fatalf("the daemon was reloaded %d times on adoption; the panel must leave it alone", ledger.reloads)
	}
}
