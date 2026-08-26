package service

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/xray"
)

/*
A traffic write that never lands must be reported as a failure.

addTrafficLocked committed in a deferred function and logged the error. Its
returns were unnamed, so the defer could not touch them: the function returned
nil after a transaction that had rolled back. AddTraffic told its caller the
poll had been persisted, the deltas were gone, and the next poll started from
counters the core had already zeroed — traffic silently unbilled, with a Warning
line as the only trace. F109.

Driven through a deferred foreign-key violation, so the statements all succeed
and only COMMIT fails. That is the shape the bug is about: an error that cannot
be seen before commit time.
*/
func TestAddTrafficReportsACommitThatFailed(t *testing.T) {
	db := durablePostgresDB(t)
	tag := "traffic-commit-" + strings.NewReplacer("/", "-", " ", "-").Replace(t.Name())
	cleanupDurableInboundFixtures(t, db, tag)

	inbound := durableTestInbound(nil, tag, 25901)
	if err := db.Create(inbound).Error; err != nil {
		t.Fatalf("seed inbound: %v", err)
	}

	installDeferredCommitFailure(t, db, "update", "durable:traffic_commit_failure", "inbounds", "durable_traffic_parent", "durable_traffic_child")

	_, _, err := (&InboundService{}).AddTraffic([]*xray.Traffic{
		{IsInbound: true, Tag: tag, Up: 1024, Down: 2048},
	}, nil)

	if err == nil {
		t.Fatal("AddTraffic reported success for a transaction that never committed: the deltas are gone and the caller believes they were persisted")
	}
	// The wording is GORM's, not ours — the deferred violation surfaces as
	// "commit unexpectedly resulted in rollback" on this path. What matters is
	// that a commit failure reaches the caller at all.
	if !strings.Contains(strings.ToLower(err.Error()), "rollback") &&
		!strings.Contains(strings.ToLower(err.Error()), "foreign key") {
		t.Fatalf("AddTraffic error = %v, want the failure from the deferred constraint", err)
	}

	// And nothing was persisted, which is what makes the silent nil so costly.
	var persisted model.Inbound
	if err := db.Where("tag = ?", tag).First(&persisted).Error; err != nil {
		t.Fatalf("reload inbound: %v", err)
	}
	if persisted.Up != 0 || persisted.Down != 0 {
		t.Fatalf("counters moved across a failed commit: up=%d down=%d", persisted.Up, persisted.Down)
	}
}

/*
And the core-quota reset runs after the commit, not inside the transaction.

resetCoreQuotas is an HTTP round trip to the mtg sidecar. It was a `defer`
registered inside addTrafficLocked, AFTER the commit/rollback defer — and Go
runs defers LIFO, so it fired BEFORE the commit, holding the traffic transaction
open across a network call on every renewal. The comment above it claimed the
opposite. F108 / F246.

Asserted on the source: both orders behave identically until the daemon is slow
or unreachable, which is exactly when it matters and exactly what a unit test
cannot stage. What can be checked is that the call is no longer deferred inside
the transaction at all.
*/
func TestTheCoreQuotaResetIsNotDeferredInsideTheTransaction(t *testing.T) {
	body := readServiceSource(t, "inbound_traffic.go")

	locked := body[strings.Index(body, "func (s *InboundService) addTrafficLocked"):]
	locked = locked[:strings.Index(locked, "\nfunc ")]
	if strings.Contains(locked, "defer resetCoreQuotas") {
		t.Fatal("resetCoreQuotas is deferred inside addTrafficLocked again: defers run LIFO, so it fires before the commit defer registered above it and holds the transaction open across a network call")
	}

	// It belongs to the caller, outside the serial writer, and only on success.
	add := body[strings.Index(body, "func (s *InboundService) AddTraffic"):]
	add = add[:strings.Index(add, "\nfunc ")]
	if !strings.Contains(add, "resetCoreQuotas(context.Background(), renewed)") {
		t.Fatal("AddTraffic no longer clears core quotas after a renewal; a renewed MTProto client stays blocked by the sidecar's own counter")
	}
	// Asserted structurally, not on a literal condition: the point is that the
	// call sits inside the success branch, whatever that branch has grown to
	// contain. An earlier version pinned the exact `if` line and went red when
	// F381 added a second post-commit step beside it.
	success := strings.Index(add, "if err == nil")
	// The CALL, not the word — the comment above it names the old shape, and
	// matching that made this test fail on correct code.
	reset := strings.Index(add, "resetCoreQuotas(context.Background(), renewed)")
	if success < 0 || reset < 0 {
		t.Fatal("AddTraffic no longer guards its post-commit work on success; this guard is now vacuous")
	}
	if reset < success {
		t.Fatal("core quotas are cleared before the write is known to have committed: clearing them for a rolled-back renewal un-blocks a client whose quota was never actually reset")
	}
}
