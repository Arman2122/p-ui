package outbound

import (
	"os"
	"strings"
	"sync"
	"testing"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/xray"
)

/*
A reset that lands between the read and the write must not be undone.

addOutboundTraffic read the row with FirstOrCreate, added the delta in Go, and
wrote the absolute result back with Save. Unlike every other traffic writer in
the panel — which uses a clamped in-place add so the DATABASE does the
arithmetic — that read-modify-write carries a stale total forward: an operator
who cleared an outbound's counters between the two statements watched them
reappear on the next poll, with the delta added on top. F111.

Staged literally: read the row the way the old code did, reset it, then let the
writer run. Under the old shape the reset is erased; under the fix the write is
an increment from whatever is there now, so the reset survives and only the
delta lands.
*/
func TestAnOutboundResetIsNotUndoneByAnInFlightPoll(t *testing.T) {
	initOutboundTestDB(t)
	db := database.GetDB()

	const tag = "atomic-outbound-probe"
	t.Cleanup(func() { _ = db.Where("tag = ?", tag).Delete(&model.OutboundTraffics{}).Error })

	// A row with history on it, which is what a reset is for.
	if err := db.Create(&model.OutboundTraffics{Tag: tag, Up: 900, Down: 100, Total: 1000}).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	// The reset fires BETWEEN the poll's read and its write. Staged with a query
	// callback rather than a goroutine so the interleaving is exact every run —
	// a sleep would make this test flaky in the direction of passing.
	resetOnce := sync.Once{}
	const callback = "test:reset_between_read_and_write"
	err := db.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement == nil || tx.Statement.Table != "outbound_traffics" {
			return
		}
		resetOnce.Do(func() {
			_ = tx.Session(&gorm.Session{NewDB: true}).
				Model(&model.OutboundTraffics{}).Where("tag = ?", tag).
				Updates(map[string]any{"up": 0, "down": 0, "total": 0}).Error
		})
	})
	if err != nil {
		t.Fatalf("register callback: %v", err)
	}
	t.Cleanup(func() { _ = db.Callback().Query().Remove(callback) })

	if err, _ := (&OutboundService{}).AddTraffic([]*xray.Traffic{
		{IsOutbound: true, Tag: tag, Up: 5, Down: 3},
	}, nil); err != nil {
		t.Fatalf("AddTraffic: %v", err)
	}

	var row model.OutboundTraffics
	if err := db.Where("tag = ?", tag).First(&row).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if row.Up != 5 || row.Down != 3 || row.Total != 8 {
		t.Fatalf("the reset was undone: up=%d down=%d total=%d, want 5/3/8 — the poll wrote an absolute value it had computed before the reset landed", row.Up, row.Down, row.Total)
	}
}

// And the ordinary case still accumulates, so the atomic add is an add.
func TestOutboundTrafficStillAccumulates(t *testing.T) {
	initOutboundTestDB(t)
	db := database.GetDB()

	const tag = "atomic-outbound-accumulate"
	t.Cleanup(func() { _ = db.Where("tag = ?", tag).Delete(&model.OutboundTraffics{}).Error })

	svc := &OutboundService{}
	for range 3 {
		if err, _ := svc.AddTraffic([]*xray.Traffic{
			{IsOutbound: true, Tag: tag, Up: 10, Down: 4},
		}, nil); err != nil {
			t.Fatalf("AddTraffic: %v", err)
		}
	}

	var row model.OutboundTraffics
	if err := db.Where("tag = ?", tag).First(&row).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if row.Up != 30 || row.Down != 12 || row.Total != 42 {
		t.Fatalf("up=%d down=%d total=%d, want 30/12/42", row.Up, row.Down, row.Total)
	}
}

// A commit that fails must be reported, not logged and discarded — the same
// defect F109 fixed on the inbound writer, in the sibling doing the same job.
func TestOutboundAddTrafficNamesItsReturnsSoACommitFailureEscapes(t *testing.T) {
	body := readOutboundSource(t, "outbound.go")
	head := body[strings.Index(body, "func (s *OutboundService) AddTraffic"):]
	head = head[:strings.Index(head, "\n}\n")]

	if !strings.Contains(head, "(err error, needRestart bool)") {
		t.Fatal("AddTraffic's returns are unnamed again: the deferred commit cannot report a failure through them, so a transaction that never landed is reported as a success")
	}
	if !strings.Contains(head, "err = cErr") {
		t.Fatal("the deferred commit no longer assigns its error to the named return")
	}
}

// readOutboundSource reads one file of this package as text.
func readOutboundSource(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(body)
}
