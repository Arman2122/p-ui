package job

import (
	"context"
	"encoding/json"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/policy"
	"github.com/Arman2122/p-ui/internal/web/runtime"
	"github.com/Arman2122/p-ui/internal/web/service"
)

// sessionFakeCore is fakeCore that also reports who is connected, which is the
// one capability the IP log is built from.
type sessionFakeCore struct {
	*fakeCore
	sessions []core.Session
}

func (c *sessionFakeCore) Sessions(context.Context) ([]core.Session, error) { return c.sessions, nil }

func installSessionCore(t *testing.T, made core.Core) {
	t.Helper()
	reg := core.NewRegistry()
	if err := reg.Register(made); err != nil {
		t.Fatalf("register %s: %v", made.Describe().ID, err)
	}
	previous := runtime.GetManager()
	runtime.SetManager(runtime.NewManager(runtime.LocalDeps{Cores: reg}))
	t.Cleanup(func() { runtime.SetManager(previous) })
}

func seedObservedClient(t *testing.T, kind core.Kind, tag string, port int, email string) {
	t.Helper()
	blob, err := json.Marshal(map[string]any{"clients": []map[string]any{
		{"email": email, "enable": true, "id": "11111111-1111-1111-1111-111111111111"},
	}})
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	inbound := &model.Inbound{
		UserId: 1, Tag: tag, Enable: true, Port: port,
		Protocol: model.Protocol(kind), Settings: string(blob),
	}
	if err := database.GetDB().Create(inbound).Error; err != nil {
		t.Fatalf("seed inbound: %v", err)
	}
	record := &model.ClientRecord{Email: email, Enable: true}
	if err := database.GetDB().Create(record).Error; err != nil {
		t.Fatalf("seed client: %v", err)
	}
	if err := database.GetDB().Create(&model.ClientInbound{ClientId: record.Id, InboundId: inbound.Id}).Error; err != nil {
		t.Fatalf("link client: %v", err)
	}
}

/*
The IP log is not part of enforcement, and the toggle used to switch off both.

PUI_ENABLE_FAIL2BAN=false means "do not ban anyone here". The pass used to
return before it had even asked the cores who was connected, so the client IP
log, /clientIps and both info modals went permanently empty on any panel with
the toggle off or fail2ban absent — a panel with no bans became a panel with no
visibility, which is the opposite of what an operator turning off automatic
enforcement is asking for.
*/
func TestObservationSurvivesTheEnforcementToggle(t *testing.T) {
	initTestDB(t)
	t.Setenv("PUI_IP_LIMIT_BANS", "false")
	t.Setenv("PUI_LOG_FOLDER", t.TempDir())
	installBanPlane(t)

	at := time.Now().Add(-5 * time.Minute)
	seen := &sessionFakeCore{
		fakeCore: &fakeCore{id: "vless"},
		sessions: []core.Session{
			{Email: "watched", Source: netip.MustParseAddr("198.51.100.4"), LastSeenUnixMilli: at.Unix() * 1000},
			{Email: "watched", Source: netip.MustParseAddr("198.51.100.5"), LastSeenUnixMilli: at.Add(time.Second).Unix() * 1000},
		},
	}
	installSessionCore(t, seen)
	seedObservedClient(t, "vless", "vless-observed", 45601, "watched")

	NewCorePolicyJob().enforceIPLimits(context.Background())

	var row model.InboundClientIps
	if err := database.GetDB().Where("client_email = ?", "watched").First(&row).Error; err != nil {
		t.Fatalf("no observed-address row for a connected client: %v — the panel's IP log is empty for as long as the toggle is off", err)
	}
	for _, address := range []string{"198.51.100.4", "198.51.100.5"} {
		if !containsAddress(row.Ips, address) {
			t.Fatalf("observed addresses %q are missing %s", row.Ips, address)
		}
	}
}

func containsAddress(blob, address string) bool {
	var entries []model.ClientIpEntry
	if json.Unmarshal([]byte(blob), &entries) == nil {
		for _, entry := range entries {
			if entry.IP == address {
				return true
			}
		}
	}
	return false
}

/*
The verdict is made whether or not this host bans; the ban is what the toggle
gates. Off, no row is recorded and the firewall is handed nothing, so a host
set not to ban drops nobody, under the toggle's new name and under the one it
had while fail2ban banned.
*/
func TestNoBanIsRecordedWhereBanningIsOff(t *testing.T) {
	over := []service.IPLimitVerdict{{
		Email:    "capped",
		Ban:      []policy.Observation{{IP: "10.0.0.9", LastSeenUnixMilli: 1786398189000}},
		BanPorts: []uint16{443},
	}}
	for _, name := range []string{"PUI_IP_LIMIT_BANS", "PUI_ENABLE_FAIL2BAN"} {
		t.Run(name+"=false", func(t *testing.T) {
			initTestDB(t)
			t.Setenv("PUI_LOG_FOLDER", t.TempDir())
			t.Setenv(name, "false")
			plane := installBanPlane(t)
			NewCorePolicyJob().reportOverLimit(context.Background(), over, time.Now())
			if rows := activeBans(t); len(rows) != 0 {
				t.Fatalf("%d ban(s) recorded on a host set not to ban", len(rows))
			}
			if last := plane.last(); strings.Contains(last, "elements") {
				t.Fatalf("the firewall was handed a ban on a host set not to ban:\n%s", last)
			}
		})
	}
	t.Run("banning on", func(t *testing.T) {
		initTestDB(t)
		t.Setenv("PUI_LOG_FOLDER", t.TempDir())
		plane := installBanPlane(t)
		NewCorePolicyJob().reportOverLimit(context.Background(), over, time.Now())
		if rows := activeBans(t); len(rows) != 1 {
			t.Fatalf("%d ban(s), want the one: the toggle decides whether the ban lands, not whether the verdict exists", len(rows))
		}
		if last := plane.last(); !strings.Contains(last, "10.0.0.9 . 443") {
			t.Fatalf("the ban was recorded and not handed to the firewall:\n%s", last)
		}
	})
}
