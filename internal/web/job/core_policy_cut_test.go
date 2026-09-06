package job

import (
	"context"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/service"
)

// cutFakeCore is sessionFakeCore that can also end one device's sessions, and
// records every cut it was asked for.
type cutFakeCore struct {
	*sessionFakeCore
	cuts []string
}

func (c *cutFakeCore) CutSessions(_ context.Context, email string, source netip.Addr) (int, error) {
	c.cuts = append(c.cuts, email+"@"+source.String())
	return 1, nil
}

// admittingFakeCore is a cutFakeCore that also refuses over-cap devices at its
// own login, the shape of a daemon core wired to the panel.
type admittingFakeCore struct{ *cutFakeCore }

func (c *admittingFakeCore) AdmitsAtLogin(kind core.Kind) bool { return kind == c.id }

// twoDevices is one client seen from two sources, the second a minute after the
// first, so the second is the one over a cap of one.
func twoDevices(email string) []core.Session {
	first := time.Now().Add(-5 * time.Minute).UnixMilli()
	return []core.Session{
		{Email: email, Source: netip.MustParseAddr("198.51.100.4"), LastSeenUnixMilli: first, StartedUnixMilli: first},
		{Email: email, Source: netip.MustParseAddr("198.51.100.5"), LastSeenUnixMilli: first + 60_000, StartedUnixMilli: first + 60_000},
	}
}

func capClient(t *testing.T, email string, limit int) {
	t.Helper()
	if err := database.GetDB().Model(&model.ClientRecord{}).Where("email = ?", email).Update("limit_ip", limit).Error; err != nil {
		t.Fatalf("cap %s: %v", email, err)
	}
}

// carryOut runs one pass the way the cron does and returns the bans this host
// holds after it, which is what the firewall is converged on.
func carryOut(t *testing.T) []model.ClientBan {
	t.Helper()
	svc := service.PolicyService{}
	verdicts, err := svc.EvaluateIPLimits(svc.ObserveSessions(context.Background()))
	if err != nil {
		t.Fatalf("EvaluateIPLimits: %v", err)
	}
	NewCorePolicyJob().reportOverLimit(context.Background(), verdicts, time.Now())
	return activeBans(t)
}

/*
The three shapes of enforcement, carried out end to end through the runtime.

A core that admits at login and can cut is cut and not banned: the cut ends the
straggler, admission refuses its return, and a firewall ban of a NATed address
would take innocents with it. A core that can cut but not admit is cut AND
banned, or the cut flaps every pass. A core that can do neither has only the
firewall, and the ban lands on the inbound's port, not on the whole host.
*/
func TestEnforcementFollowsWhatTheObservingCoreCanDo(t *testing.T) {
	t.Run("admits and cuts: cut, not banned", func(t *testing.T) {
		initTestDB(t)
		t.Setenv("PUI_LOG_FOLDER", t.TempDir())
		installBanPlane(t)
		fake := &admittingFakeCore{cutFakeCore: &cutFakeCore{sessionFakeCore: &sessionFakeCore{fakeCore: &fakeCore{id: "ssh"}}}}
		installSessionCore(t, fake)
		seedObservedClient(t, "ssh", "ssh-capped", 45701, "watched")
		capClient(t, "watched", 1)
		fake.sessions = twoDevices("watched")

		bans := carryOut(t)
		if len(fake.cuts) != 1 || fake.cuts[0] != "watched@198.51.100.5" {
			t.Fatalf("want the latest arrival cut once through the runtime, got %v", fake.cuts)
		}
		if len(bans) != 0 {
			t.Fatalf("a core that admits at login needs no firewall behind its cut, yet %d ban(s) were recorded", len(bans))
		}
	})

	t.Run("cuts but does not admit: cut and banned", func(t *testing.T) {
		initTestDB(t)
		t.Setenv("PUI_LOG_FOLDER", t.TempDir())
		installBanPlane(t)
		fake := &cutFakeCore{sessionFakeCore: &sessionFakeCore{fakeCore: &fakeCore{id: "tuic"}}}
		installSessionCore(t, fake)
		seedObservedClient(t, "tuic", "tuic-capped", 45702, "watched")
		capClient(t, "watched", 1)
		fake.sessions = twoDevices("watched")

		bans := carryOut(t)
		if len(fake.cuts) != 1 || fake.cuts[0] != "watched@198.51.100.5" {
			t.Fatalf("want the latest arrival cut once, got %v", fake.cuts)
		}
		if len(bans) != 1 {
			t.Fatalf("a cut without admission must be backed by the firewall: %d ban(s), want 1", len(bans))
		}
	})

	t.Run("neither: left to the firewall, on its port", func(t *testing.T) {
		initTestDB(t)
		t.Setenv("PUI_LOG_FOLDER", t.TempDir())
		plane := installBanPlane(t)
		fake := &sessionFakeCore{fakeCore: &fakeCore{id: "vless"}}
		installSessionCore(t, fake)
		seedObservedClient(t, "vless", "vless-capped", 45703, "watched")
		capClient(t, "watched", 1)
		fake.sessions = twoDevices("watched")

		bans := carryOut(t)
		if len(bans) != 1 || bans[0].Source != "198.51.100.5" || bans[0].Ports != "45703" {
			t.Fatalf("nothing here can cut an Xray session, so the firewall is the enforcement: %+v, want one ban of 198.51.100.5 on 45703", bans)
		}
		if !strings.Contains(plane.last(), "198.51.100.5 . 45703") {
			t.Fatalf("the ban must land on the inbound's port, not the host:\n%s", plane.last())
		}
	})
}
