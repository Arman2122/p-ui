package job

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/banset"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/policy"
	"github.com/Arman2122/p-ui/internal/web/service"
)

// recordingBanPlane stands in for the host firewall and keeps every script it
// was handed, the last of which is what the kernel would hold now.
type recordingBanPlane struct{ scripts []string }

func (p *recordingBanPlane) Probe(context.Context) error { return nil }

func (p *recordingBanPlane) Apply(_ context.Context, script string) error {
	p.scripts = append(p.scripts, script)
	return nil
}

func (p *recordingBanPlane) last() string {
	if len(p.scripts) == 0 {
		return ""
	}
	return p.scripts[len(p.scripts)-1]
}

func installBanPlane(t *testing.T) *recordingBanPlane {
	t.Helper()
	plane := &recordingBanPlane{}
	t.Cleanup(service.UseBanManager(banset.NewManager(plane)))
	return plane
}

// activeBans is every ban this host holds, which is the memory the pass keeps
// and the want the firewall is converged on.
func activeBans(t *testing.T) []model.ClientBan {
	t.Helper()
	var rows []model.ClientBan
	if err := database.GetDB().Order("id").Find(&rows).Error; err != nil {
		t.Fatalf("read bans: %v", err)
	}
	return rows
}

func overCapVerdict(email, source string, port uint16) []service.IPLimitVerdict {
	return []service.IPLimitVerdict{{
		Email:    email,
		Ban:      []policy.Observation{{IP: source, LastSeenUnixMilli: 1786398189000}},
		BanPorts: []uint16{port},
	}}
}

/*
A device banned is a device disconnected, so the very next pass finds its client
back under the cap. That pass must not release the ban, the deadline does, or
the device reconnects ten seconds later and the cap enforces nothing; and once
the deadline has passed and the device is over the cap again, a new ban must
follow. The three passes are what the cron does: over, under, over again later.
*/
func TestABanOutlivesThePassAndIsRecordedAgainOnceItLapsed(t *testing.T) {
	initTestDB(t)
	t.Setenv("PUI_LOG_FOLDER", t.TempDir())
	plane := installBanPlane(t)
	job := NewCorePolicyJob()
	over := overCapVerdict("alice", "10.0.0.9", 443)
	under := []service.IPLimitVerdict{{Email: "alice"}}
	t0 := time.Now()

	job.reportOverLimit(context.Background(), over, t0)
	first := activeBans(t)
	if len(first) != 1 || first[0].Until != t0.Unix()+30*60 {
		t.Fatalf("after the over-cap pass: %+v, want one ban for the operator's default of 30 minutes", first)
	}
	if !strings.Contains(plane.last(), "10.0.0.9 . 443") {
		t.Fatalf("the ban was not handed to the firewall:\n%s", plane.last())
	}

	job.reportOverLimit(context.Background(), under, t0.Add(10*time.Second))
	if rows := activeBans(t); len(rows) != 1 || rows[0].Id != first[0].Id {
		t.Fatalf("a pass under the cap released the ban: %+v; the deadline releases it, or the device is back in ten seconds", rows)
	}

	job.reportOverLimit(context.Background(), over, t0.Add(31*time.Minute))
	rows := activeBans(t)
	if len(rows) != 1 || rows[0].Id == first[0].Id {
		t.Fatalf("over the cap again after the deadline: %+v, want a new ban", rows)
	}
}

// A device still over its cap on the next pass is the same ban, not a longer one.
func TestAPairIsNotBannedTwiceWithinOneBan(t *testing.T) {
	initTestDB(t)
	t.Setenv("PUI_LOG_FOLDER", t.TempDir())
	installBanPlane(t)
	job := NewCorePolicyJob()
	over := overCapVerdict("alice", "10.0.0.9", 443)
	t0 := time.Now()

	job.reportOverLimit(context.Background(), over, t0)
	job.reportOverLimit(context.Background(), over, t0.Add(10*time.Second))
	rows := activeBans(t)
	if len(rows) != 1 {
		t.Fatalf("%d ban(s) for one pair, want 1", len(rows))
	}
	if rows[0].Until != t0.Unix()+30*60 {
		t.Fatalf("the second pass moved the deadline to %d, want %d: a ban is not extended by the device staying over the cap",
			rows[0].Until, t0.Unix()+30*60)
	}
}

// The kernel lets an element lapse on its own; the pass must agree, pruning the
// row and, with nothing left, taking the table down with it.
func TestABanPastItsDeadlineIsPrunedAndTheTableWithIt(t *testing.T) {
	initTestDB(t)
	t.Setenv("PUI_LOG_FOLDER", t.TempDir())
	plane := installBanPlane(t)
	t0 := time.Now()
	lapsed := model.ClientBan{ClientEmail: "alice", Source: "10.0.0.9", Ports: "443", Reason: "over-cap", CreatedAt: t0.Add(-time.Hour).Unix(), Until: t0.Add(-time.Minute).Unix()}
	if err := database.GetDB().Create(&lapsed).Error; err != nil {
		t.Fatalf("seed ban: %v", err)
	}

	NewCorePolicyJob().reportOverLimit(context.Background(), nil, t0)
	if rows := activeBans(t); len(rows) != 0 {
		t.Fatalf("a ban past its deadline is still held: %+v", rows)
	}
	last := plane.last()
	if last == "" || strings.Contains(last, "elements") || !strings.Contains(last, "delete table inet "+banset.Table) {
		t.Fatalf("the last ban leaving must take the table with it, got:\n%s", last)
	}
}
