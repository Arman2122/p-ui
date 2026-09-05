package ocserv

import (
	"context"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/supervisor"
)

/*
A daemon that died stayed dead: applyLocked returned on the flag it set at
start, so the exec backend — which restarts nothing on its own — was never
asked again, and under systemd a unit past its start limit sat in failed. The
backend is asked now: a unit it reports down is started, and an unchanged
running one is still left alone.
*/
func TestADeadDaemonIsStartedAgain(t *testing.T) {
	ledger := newUnitLedger()
	c := New(Deps{Backend: ledger, Binary: "/usr/sbin/ocserv", Occtl: "/usr/bin/occtl", StateDir: t.TempDir()})
	ctx := context.Background()
	settings, err := c.MintInstance(Kind, `{"subnet":"10.11.0.0/24"}`, "")
	if err != nil {
		t.Fatal(err)
	}
	inst := core.Instance{ID: 3, Kind: Kind, Tag: "ocs-3", Port: 8443, Enable: true, Settings: settings}
	unit := supervisor.UnitName(string(ID), 3)
	for range 2 {
		if err := c.Reconcile(ctx, []core.Instance{inst}); err != nil {
			t.Fatal(err)
		}
	}
	if ledger.ensures != 1 {
		t.Fatalf("ensures = %d after two reconciles of one unchanged inbound; want 1", ledger.ensures)
	}

	ledger.die(unit)
	if err := c.Reconcile(ctx, []core.Instance{inst}); err != nil {
		t.Fatal(err)
	}
	if ledger.ensures != 2 {
		t.Fatalf("ensures = %d after the daemon died; want 2 — the reconcile left a dead inbound dead", ledger.ensures)
	}
	if st, err := ledger.Status(ctx, unit); err != nil || st.State != supervisor.StateRunning {
		t.Fatalf("status = %+v, %v; want running", st, err)
	}
}
