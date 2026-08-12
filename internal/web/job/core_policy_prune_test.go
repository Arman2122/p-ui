package job

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/policy"
	"github.com/Arman2122/p-ui/internal/web/service"
	"github.com/Arman2122/p-ui/internal/xray"
)

// banLines is every fail2ban line this pass wrote, which is the only thing a
// jail on a deployed box ever acts on.
func banLines(t *testing.T) []string {
	t.Helper()
	blob, err := os.ReadFile(xray.GetIPLimitLogPath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("read the ip limit log: %v", err)
	}
	var out []string
	for _, line := range strings.Split(string(blob), "\n") {
		if strings.Contains(line, "[LIMIT_IP]") {
			out = append(out, line)
		}
	}
	return out
}

/*
TestAReBanIsReportedAgainAfterAPassUnderTheCap.

The re-ban dedup rests on lastSeen ADVANCING, because a frozen value is a dead
connection the daemon has not reaped rather than a reconnect. That is correct
only while the pair is forgotten once the client goes back under its cap: an idle
but open connection never advances its timestamp, so a pair that is remembered
forever can never be reported again once fail2ban's own bantime lapses, and the
cap silently stops being enforced while the UI still shows it configured.

The three passes are exactly what the cron does: over cap, under cap, over cap
again with an UNCHANGED timestamp.
*/
func TestAReBanIsReportedAgainAfterAPassUnderTheCap(t *testing.T) {
	t.Setenv("PUI_LOG_FOLDER", t.TempDir())
	job := NewCorePolicyJob()

	const frozen = int64(1786398189000)
	over := []service.IPLimitVerdict{{
		Email: "alice",
		Ban:   []policy.Observation{{IP: "10.0.0.9", LastSeenUnixMilli: frozen}},
	}}
	// The client is back inside its cap: still evaluated, nothing to ban. This is
	// the pass that must forget the pair.
	under := []service.IPLimitVerdict{{Email: "alice"}}

	job.reportOverLimit(context.Background(), over)
	if got := len(banLines(t)); got != 1 {
		t.Fatalf("after the first over-cap pass: %d line(s), want 1", got)
	}
	job.reportOverLimit(context.Background(), under)
	if got := len(banLines(t)); got != 1 {
		t.Fatalf("an under-cap pass must report nothing: %d line(s), want 1", got)
	}
	job.reportOverLimit(context.Background(), over)

	lines := banLines(t)
	if len(lines) != 2 {
		t.Fatalf("the address is over the cap again and fail2ban's own ban has lapsed, so it must be re-reported: %d line(s), want 2\n%s",
			len(lines), strings.Join(lines, "\n"))
	}
	for _, line := range lines {
		if !strings.Contains(line, "Disconnecting OLD IP = 10.0.0.9") {
			t.Fatalf("unexpected line %q", line)
		}
	}
}

/*
TestAPairIsNotReportedTwiceWithinOneBan is the other half.

Without it, "report it again" could pass by reporting every pass forever, which
is the failure the dedup exists to prevent: fail2ban would keep re-banning an
address that is merely still open.
*/
func TestAPairIsNotReportedTwiceWithinOneBan(t *testing.T) {
	t.Setenv("PUI_LOG_FOLDER", t.TempDir())
	job := NewCorePolicyJob()

	over := []service.IPLimitVerdict{{
		Email: "alice",
		Ban:   []policy.Observation{{IP: "10.0.0.9", LastSeenUnixMilli: 1786398189000}},
	}}
	job.reportOverLimit(context.Background(), over)
	job.reportOverLimit(context.Background(), over)

	if got := len(banLines(t)); got != 1 {
		t.Fatalf("a frozen lastSeen is a connection the daemon has not reaped, not a reconnect: %d line(s), want 1", got)
	}
}

// TestTheRememberedPairIsReleased pins the memory itself, so the prune cannot be
// satisfied by a log-line coincidence.
func TestTheRememberedPairIsReleased(t *testing.T) {
	t.Setenv("PUI_LOG_FOLDER", t.TempDir())
	job := NewCorePolicyJob()

	job.reportOverLimit(context.Background(), []service.IPLimitVerdict{{
		Email: "alice",
		Ban:   []policy.Observation{{IP: "10.0.0.9", LastSeenUnixMilli: 1786398189000}},
	}})
	if len(job.bannedSeen) != 1 {
		t.Fatalf("the banned pair must be remembered, got %v", job.bannedSeen)
	}
	job.reportOverLimit(context.Background(), []service.IPLimitVerdict{{Email: "alice"}})
	if len(job.bannedSeen) != 0 {
		t.Fatalf("a client back under its cap must release every pair it held, got %v", job.bannedSeen)
	}
}

// The log path is derived, so a test that wrote somewhere else would prove
// nothing about the file a jail watches.
func TestTheBanLogGoesWhereTheJailWatches(t *testing.T) {
	folder := t.TempDir()
	t.Setenv("PUI_LOG_FOLDER", folder)
	if got, want := xray.GetIPLimitLogPath(), filepath.Join(folder, "pui-ipl.log"); filepath.Clean(got) != want {
		t.Fatalf("ban log path = %q, want %q", got, want)
	}
}
