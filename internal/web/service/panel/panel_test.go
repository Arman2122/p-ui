package panel

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/v3/internal/config"
	"github.com/Arman2122/p-ui/v3/internal/web/service"
)

func TestIsNewerVersion(t *testing.T) {
	cases := []struct {
		latest  string
		current string
		want    bool
	}{
		{"v2.9.4", "2.9.3", true},
		{"v2.10.0", "2.9.9", true},
		{"v2.9.3", "2.9.3", false},
		{"v2.9.2", "2.9.3", false},
		{"v3.0.0", "2.9.3", true},
	}

	for _, tc := range cases {
		if got := isNewerVersion(tc.latest, tc.current); got != tc.want {
			t.Fatalf("isNewerVersion(%q, %q) = %v, want %v", tc.latest, tc.current, got, tc.want)
		}
	}
}

func TestCompareVersionStringsRejectsUnexpectedFormats(t *testing.T) {
	if _, ok := compareVersionStrings("latest", "2.9.3"); ok {
		t.Fatal("expected non-semver latest tag to be rejected")
	}
	if _, ok := compareVersionStrings("v2.9", "2.9.3"); ok {
		t.Fatal("expected short version to be rejected")
	}
}

func TestShellQuote(t *testing.T) {
	if got := shellQuote("/usr/bin/curl"); got != "'/usr/bin/curl'" {
		t.Fatalf("unexpected quote result: %s", got)
	}
	if got := shellQuote("/tmp/a'b"); got != "'/tmp/a'\\''b'" {
		t.Fatalf("unexpected quote result with single quote: %s", got)
	}
}

func TestExtractReleaseCommit(t *testing.T) {
	full := "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b"
	cases := []struct {
		name    string
		release service.Release
		want    string
	}{
		{
			name:    "from body marker",
			release: service.Release{Body: "Rolling build\n\ncommit=" + full + "\nbuilt=2026-06-24T00:00:00Z"},
			want:    full,
		},
		{
			name:    "body marker is case-insensitive and wins over target",
			release: service.Release{Body: "COMMIT=" + full, TargetCommitish: "deadbeef"},
			want:    full,
		},
		{
			name:    "fallback to target commit sha",
			release: service.Release{Body: "no marker here", TargetCommitish: full},
			want:    full,
		},
		{
			name:    "branch target is not a commit",
			release: service.Release{Body: "no marker", TargetCommitish: "main"},
			want:    "",
		},
	}
	for _, tc := range cases {
		if got := extractReleaseCommit(&tc.release); got != tc.want {
			t.Fatalf("%s: extractReleaseCommit = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestCommitsEqual(t *testing.T) {
	full := "1a2b3c4d5e6f7a8b9c0d1e2f3a4b5c6d7e8f9a0b"
	cases := []struct {
		a, b string
		want bool
	}{
		{"1a2b3c4d", full, true},  // injected 8-char prefix matches full release sha
		{full, "1a2b3c4d", true},  // order independent
		{"1A2B3C4D", full, true},  // case insensitive
		{"deadbeef", full, false}, // different commit
		{"", full, false},         // empty current never matches
		{"1a2b3c4d", "", false},   // empty latest never matches
	}
	for _, tc := range cases {
		if got := commitsEqual(tc.a, tc.b); got != tc.want {
			t.Fatalf("commitsEqual(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestShortCommit(t *testing.T) {
	if got := shortCommit("1a2b3c4d5e6f7a8b"); got != "1a2b3c4d" {
		t.Fatalf("shortCommit truncation = %q, want %q", got, "1a2b3c4d")
	}
	if got := shortCommit("abc"); got != "abc" {
		t.Fatalf("shortCommit short input = %q, want %q", got, "abc")
	}
}

func resetUpdateSlot(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		updateMu.Lock()
		updateRunning = false
		updateRunID = 0
		updatePID = 0
		updateMu.Unlock()
	})
}

// isolateUpdateStatusFile prepares the panel self-update status file for one
// test and reports whether this process can actually write it.
//
// config.GetUpdateStatusFilePath() is a fixed absolute path under the panel's
// persistent state folder (/etc/p-ui) with no test override, so these tests can
// only exercise the real location. This helper makes that safe and
// deterministic:
//   - any pre-existing file is cleared and restored on cleanup, so a run on a
//     host that really has the panel installed neither reads nor leaves behind
//     production state;
//   - the file always starts out absent and never survives the test, so neither
//     the "missing status file" case nor acquireUpdateSlot's terminal-status
//     check can be decided by what a sibling test left behind -- the order
//     those run in is randomized by `go test -shuffle=on` in CI;
//   - a missing or read-only state folder is reported rather than fatal. That is
//     the normal case on a CI runner (no /etc/p-ui, and the test process is not
//     root); callers that must write the file skip through
//     requireWritableUpdateStatusFile instead of failing on ENOENT/EACCES.
func isolateUpdateStatusFile(t *testing.T) (string, bool) {
	t.Helper()

	path := config.GetUpdateStatusFilePath()
	saved, readErr := os.ReadFile(path)
	switch {
	case readErr == nil:
		if err := os.Remove(path); err != nil {
			t.Skipf("cannot clear the pre-existing %s: %v", path, err)
		}
		t.Cleanup(func() { _ = os.WriteFile(path, saved, 0o644) })
	case errors.Is(readErr, os.ErrNotExist):
		t.Cleanup(func() { _ = os.Remove(path) })
	default:
		t.Skipf("cannot inspect %s: %v", path, readErr)
	}

	writable := false
	if probe, err := os.CreateTemp(filepath.Dir(path), ".update-status-probe-*"); err == nil {
		name := probe.Name()
		_ = probe.Close()
		_ = os.Remove(name)
		writable = true
	}
	return path, writable
}

// requireWritableUpdateStatusFile is isolateUpdateStatusFile for the tests that
// have to write the status file themselves, skipping when the state folder
// cannot be written.
func requireWritableUpdateStatusFile(t *testing.T) string {
	t.Helper()
	path, writable := isolateUpdateStatusFile(t)
	if !writable {
		t.Skipf("cannot write %s: the panel state folder is missing or read-only for this process, and the path has no test override", path)
	}
	return path
}

// writeStatusFile hand-writes the status file in the exact wire format
// update.sh itself produces (a bare printf, not Go's json.Marshal), since
// that's the real cross-language contract this package reads in production.
func writeStatusFile(t *testing.T, path string, runID int64, state string) {
	t.Helper()
	body := fmt.Sprintf(`{"runId":"%d","state":"%s","exitCode":0,"finishedAt":%d}`, runID, state, time.Now().Unix())
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAcquireUpdateSlot(t *testing.T) {
	resetUpdateSlot(t)

	if !acquireUpdateSlot(1) {
		t.Fatal("first acquire: got false, want true")
	}
	if acquireUpdateSlot(2) {
		t.Fatal("second acquire while first is held: got true, want false")
	}
	releaseUpdateSlot()
	if !acquireUpdateSlot(3) {
		t.Fatal("acquire after release: got false, want true")
	}
	releaseUpdateSlot()
}

func TestAcquireUpdateSlotExpiresAfterStaleWindow(t *testing.T) {
	resetUpdateSlot(t)

	if !acquireUpdateSlot(1) {
		t.Fatal("first acquire: got false, want true")
	}
	updateMu.Lock()
	updateStarted = time.Now().Add(-(updateStaleAfter + time.Second))
	updateMu.Unlock()

	if !acquireUpdateSlot(2) {
		t.Fatal("acquire after stale window elapsed: got false, want true")
	}
	releaseUpdateSlot()
}

// TestAcquireUpdateSlotWaitsForAliveProcessPastStaleWindow is the regression
// test for the concurrency bug an upstream review found: past
// updateStaleAfter, the old logic freed the slot purely on elapsed time, even
// if the process it launched was still genuinely running (not crashed) --
// update.sh's own package-manager step plus several downloads can plausibly
// run long on a slow host with nothing actually wrong. Now a confirmed-alive
// PID keeps the slot held past the stale window.
func TestAcquireUpdateSlotWaitsForAliveProcessPastStaleWindow(t *testing.T) {
	resetUpdateSlot(t)

	if !acquireUpdateSlot(1) {
		t.Fatal("first acquire: got false, want true")
	}
	recordUpdatePID(os.Getpid()) // the test process itself: guaranteed alive
	updateMu.Lock()
	updateStarted = time.Now().Add(-(updateStaleAfter + time.Second))
	updateMu.Unlock()

	if acquireUpdateSlot(2) {
		t.Fatal("acquire past the stale window while the recorded PID is still alive: got true, want false")
	}
	releaseUpdateSlot()
}

// TestAcquireUpdateSlotHardCeilingOverridesLiveness confirms the absolute
// backstop: even a confirmed-alive process can't hold the slot forever, so a
// genuinely wedged run can't lock out retries permanently.
func TestAcquireUpdateSlotHardCeilingOverridesLiveness(t *testing.T) {
	resetUpdateSlot(t)

	if !acquireUpdateSlot(1) {
		t.Fatal("first acquire: got false, want true")
	}
	recordUpdatePID(os.Getpid())
	updateMu.Lock()
	updateStarted = time.Now().Add(-(updateHardCeiling + time.Second))
	updateMu.Unlock()

	if !acquireUpdateSlot(2) {
		t.Fatal("acquire past the hard ceiling despite a live PID: got false, want true")
	}
	releaseUpdateSlot()
}

// TestAcquireUpdateSlotReleasesOnTerminalStatus is the regression test for the
// bug adversarial review found: a fast failure used to still lock out retries
// for the full updateStaleAfter window, because acquireUpdateSlot only looked
// at the in-memory started-at timestamp, never at the status file's own
// terminal state.
func TestAcquireUpdateSlotReleasesOnTerminalStatus(t *testing.T) {
	resetUpdateSlot(t)
	path := requireWritableUpdateStatusFile(t)

	if !acquireUpdateSlot(111) {
		t.Fatal("first acquire: got false, want true")
	}
	writeStatusFile(t, path, 111, updateStateFailed)

	if !acquireUpdateSlot(222) {
		t.Fatal("acquire after the in-flight run reported failed: got false, want true (should not wait out updateStaleAfter)")
	}
	releaseUpdateSlot()
}

// TestAcquireUpdateSlotIgnoresStaleUnrelatedStatus confirms the terminal-state
// check is scoped to the run it actually launched: a status file left behind
// by some earlier, unrelated run (different runID) must not be mistaken for
// this run finishing.
func TestAcquireUpdateSlotIgnoresStaleUnrelatedStatus(t *testing.T) {
	resetUpdateSlot(t)
	path := requireWritableUpdateStatusFile(t)

	writeStatusFile(t, path, 999, updateStateSuccess)
	if !acquireUpdateSlot(111) {
		t.Fatal("first acquire: got false, want true")
	}

	if acquireUpdateSlot(222) {
		t.Fatal("acquire while status file only reflects an unrelated older runID: got true, want false")
	}
	releaseUpdateSlot()
}

// TestAcquireUpdateSlotConcurrency proves the check-then-set is actually
// atomic under real concurrent access, not just correct when called
// sequentially. A prior version of this test suite only ever called
// acquireUpdateSlot from a single goroutine, so it gave no signal if the
// mutex's core promise (only one concurrent launch wins) were broken.
func TestAcquireUpdateSlotConcurrency(t *testing.T) {
	resetUpdateSlot(t)

	const attempts = 200
	var wins atomic.Int32
	var wg sync.WaitGroup
	wg.Add(attempts)
	for i := range attempts {
		go func(runID int64) {
			defer wg.Done()
			if acquireUpdateSlot(runID) {
				wins.Add(1)
			}
		}(int64(i))
	}
	wg.Wait()

	if got := wins.Load(); got != 1 {
		t.Fatalf("concurrent acquireUpdateSlot: %d of %d attempts won, want exactly 1", got, attempts)
	}
	releaseUpdateSlot()
}

func TestGetUpdateStatus(t *testing.T) {
	path, writable := isolateUpdateStatusFile(t)
	svc := &PanelService{}

	if got := svc.GetUpdateStatus(); got.State != updateStatePending {
		t.Fatalf("missing status file: State = %q, want %q", got.State, updateStatePending)
	}

	if !writable {
		t.Skipf("the remaining cases have to write %s, and the panel state folder is missing or read-only for this process", path)
	}

	writeStatusFile(t, path, 1735689600123456789, updateStateSuccess)
	got := svc.GetUpdateStatus()
	if got.RunID != "1735689600123456789" {
		t.Fatalf("RunID = %q, want %q (must round-trip as a decimal string, not a JSON number, or it loses precision past 2^53 in JS)", got.RunID, "1735689600123456789")
	}
	if got.State != updateStateSuccess {
		t.Fatalf("State = %q, want %q", got.State, updateStateSuccess)
	}

	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := svc.GetUpdateStatus(); got.State != updateStatePending {
		t.Fatalf("corrupt status file: State = %q, want %q", got.State, updateStatePending)
	}

	writeStatusFile(t, path, 1, "some-unrecognized-state")
	if got := svc.GetUpdateStatus(); got.State != updateStatePending {
		t.Fatalf("unrecognized state normalizes to pending: State = %q, want %q", got.State, updateStatePending)
	}
}
