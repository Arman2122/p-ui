package job

import "testing"

/*
A sync the guard refused must not become the thing the next one is measured
against.

lastFlagCount was written at the tail of every run that got as far as a
successful fetch, refused or not. So the guard fired exactly once: a directory
that came back with three users instead of five hundred was correctly refused,
the three was stored as the new baseline, and the next tick — three against a
previous of three — passed the retention test and detached every client that was
not one of those three.

The guard did not fail to fire. It fired, and then destroyed the evidence it
needed to fire again.
*/
func TestARefusedSyncDoesNotBecomeTheBaseline(t *testing.T) {
	job := NewLdapSyncJob()

	// A healthy directory, recorded.
	if !job.fetchLooksComplete(500) {
		t.Fatal("a full fetch on a fresh job should be believable")
	}
	job.recordBaseline(500)

	// The directory half-collapses. Believable? No.
	const degraded = 3
	if job.fetchLooksComplete(degraded) {
		t.Fatalf("%d users against a baseline of 500 was believed", degraded)
	}
	if job.autoDeleteAllowed(degraded) {
		t.Fatal("the degraded sync was allowed to delete")
	}
	job.recordBaseline(degraded) // a no-op, and that is the fix

	// The tick after. This is the one that used to delete everyone.
	if job.autoDeleteAllowed(degraded) {
		t.Fatalf("the second degraded tick was allowed to delete: the refused fetch became the baseline, so %d was measured against %d and passed",
			degraded, degraded)
	}
	if got := job.lastFlagCount.Load(); got != 500 {
		t.Fatalf("baseline is %d, want the last believable count 500", got)
	}
}

/*
And the first sync of a process, which is what a directory over the page limit
gets the day paging starts working.

lastFlagCount is in memory, so every panel start is a first sync: there is no
baseline, nothing to refuse against, and the retention test cannot bite. Before
this, that meant a year of failing ticks followed by one successful paged fetch
would delete every client the operator had ever made by hand.

Now the first believable sync records the baseline and deletes nothing.
*/
func TestTheFirstSyncOfAProcessNeverDeletes(t *testing.T) {
	job := NewLdapSyncJob()

	// A year of error ticks leaves the baseline where it started: unset.
	if got := job.lastFlagCount.Load(); got != 0 {
		t.Fatalf("a fresh job starts with a baseline of %d, want 0", got)
	}

	// The first fetch that works, and it is a big one.
	const firstPagedFetch = 1500
	if !job.fetchLooksComplete(firstPagedFetch) {
		t.Fatal("the first paged fetch should be believable")
	}
	if job.autoDeleteAllowed(firstPagedFetch) {
		t.Fatal("the first sync of the process was allowed to delete; there is no baseline to judge it against, so it would remove every client not in the directory")
	}

	job.recordBaseline(firstPagedFetch)
	if got := job.lastFlagCount.Load(); got != firstPagedFetch {
		t.Fatalf("baseline is %d, want %d — the first sync must still record one", got, firstPagedFetch)
	}

	// The second may.
	if !job.autoDeleteAllowed(firstPagedFetch) {
		t.Fatal("the second sync is judged against a real baseline and must be allowed")
	}
}

/*
The way out of a legitimate mass off-boarding, which the guard would otherwise
refuse forever: the baseline never updates because the fetch is never believed,
and the fetch is never believed because the baseline never updates.

It is in memory, so a panel restart clears it — and the first sync after that
records the smaller directory without deleting, per the test above.
*/
func TestARestartIsTheWayOutOfAPermanentRefusal(t *testing.T) {
	job := NewLdapSyncJob()
	job.recordBaseline(500)

	const afterOffboarding = 40
	if job.fetchLooksComplete(afterOffboarding) {
		t.Fatal("a 92% shrink should not be believed against the old baseline")
	}

	// The restart.
	restarted := NewLdapSyncJob()
	if !restarted.fetchLooksComplete(afterOffboarding) {
		t.Fatal("after a restart there is no baseline to refuse against, so the smaller directory must be believable")
	}
	if restarted.autoDeleteAllowed(afterOffboarding) {
		t.Fatal("and it still must not delete on that first sync")
	}
	restarted.recordBaseline(afterOffboarding)
	if !restarted.autoDeleteAllowed(afterOffboarding) {
		t.Fatal("the sync after it is judged against the new baseline and must be allowed")
	}
}
