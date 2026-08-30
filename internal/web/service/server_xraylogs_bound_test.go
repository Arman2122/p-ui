package service

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

/*
Opening the log viewer must cost the requested page, not the whole file.

GetXrayLogs appended every line that survived its filters into one slice and
applied the caller's row count only after the scan finished, so the peak working
set was a function of the access log's size. Xray's own rotation cap is 64 MiB
and an operator with debug logging on reaches it in hours; on a 1 GB VPS,
asking for the newest 20 lines parsed the entire file into memory first, and the
panel was killed by the OOM reaper for opening a modal.

The count was also read with `strconv.Atoi(count)` discarding its error, so a
count of 0 or a garbage string became 0 -- and `len(entries) > 0` then sliced
everything away, returning nothing after doing all the work. An absurdly large
count defeated the trim entirely.

Asserted through cap(), because the length was always right; it was the
allocation behind it that was the bug. F350.
*/

func writeAccessLog(t *testing.T, lines int) {
	t.Helper()
	binDir := t.TempDir()
	t.Setenv("PUI_BIN_FOLDER", binDir)

	logPath := filepath.Join(t.TempDir(), "access.log")
	var b strings.Builder
	for i := range lines {
		fmt.Fprintf(&b, "2026/08/27 12:00:00 10.0.0.1:%d accepted tcp:example%d.com:443 [inbound-1 >> direct]\n", 40000+i, i)
	}
	if err := os.WriteFile(logPath, []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write access log: %v", err)
	}

	config, err := json.Marshal(map[string]any{"log": map[string]any{"access": logPath, "error": ""}})
	if err != nil {
		t.Fatalf("marshal xray config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "config.json"), config, 0o644); err != nil {
		t.Fatalf("write xray config: %v", err)
	}
}

func TestGetXrayLogsHoldsThePageNotTheFile(t *testing.T) {
	const lines = 20000
	const want = 20
	writeAccessLog(t, lines)

	svc := &ServerService{}
	got := svc.GetXrayLogs(fmt.Sprint(want), "", "true", "true", "true", []string{"direct"}, nil)

	if len(got) != want {
		t.Fatalf("returned %d entries, want %d", len(got), want)
	}
	// The bug is the allocation, not the length. cap here counts only the slots
	// at or after the page start, so it is a LOWER bound on what was retained.
	if cap(got) > 4*want {
		t.Errorf("a %d-row request over a %d-line file left at least %d entry slots live past the page start; the scan retained the file instead of the page",
			want, lines, cap(got))
	}
	// And it is still the NEWEST rows, in order -- the bound must not cost that.
	if !strings.Contains(got[len(got)-1].ToAddress, fmt.Sprintf("example%d.com", lines-1)) {
		t.Errorf("last entry is %q, want the final line of the file", got[len(got)-1].ToAddress)
	}
	if !strings.Contains(got[0].ToAddress, fmt.Sprintf("example%d.com", lines-want)) {
		t.Errorf("first entry is %q, want the page to start %d lines from the end", got[0].ToAddress, want)
	}
}

func TestGetXrayLogsRefusesACountItCannotServe(t *testing.T) {
	writeAccessLog(t, 50)
	svc := &ServerService{}

	// GetLogs enforces exactly this range on the same parameter; an unbounded
	// count is what let the trim never fire.
	for _, count := range []string{"0", "-1", "abc", "", "10001"} {
		if got := svc.GetXrayLogs(count, "", "true", "true", "true", []string{"direct"}, nil); got != nil {
			t.Errorf("count %q returned %d entries; it must be refused like GetLogs refuses it", count, len(got))
		}
	}
	if got := svc.GetXrayLogs("10000", "", "true", "true", "true", []string{"direct"}, nil); got == nil {
		t.Error("count 10000 is the documented maximum and must still be served")
	}
}

/*
And the panel's own log view says when the count is the problem.

GetLogs had two branches and validated one of them. The syslog branch refused a
count outside 1..10000 with a message; the panel-log branch took whatever
strconv.Atoi returned with the error discarded, and logger.GetLogs stops as soon
as len(output) < c is false — so "abc", "0" and "-5" all produced an empty log
view. Nothing said the count was the reason, and the log view is where an
operator goes precisely when something else has already gone wrong. F343.
*/
func TestPanelLogsRefuseACountTheyCannotUse(t *testing.T) {
	svc := &ServerService{}

	for _, count := range []string{"abc", "0", "-5", "10001", ""} {
		t.Run("count="+count, func(t *testing.T) {
			got := svc.GetLogs(count, "info", "false")
			if len(got) != 1 || !strings.Contains(got[0], "Invalid count parameter") {
				t.Fatalf("GetLogs(%q) = %#v; want the same refusal the syslog branch already gives, not an empty view", count, got)
			}
		})
	}

	// And a usable count still reads the buffer rather than being refused.
	if got := svc.GetLogs("20", "info", "false"); len(got) == 1 && strings.Contains(got[0], "Invalid count parameter") {
		t.Fatalf("a valid count was refused: %#v", got)
	}
}
