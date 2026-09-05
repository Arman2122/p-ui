package job

import (
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/policy"
	"github.com/Arman2122/p-ui/internal/web/runtime"
	"github.com/Arman2122/p-ui/internal/web/service"
	"github.com/Arman2122/p-ui/internal/xray"
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
	t.Setenv("PUI_ENABLE_FAIL2BAN", "false")
	t.Setenv("PUI_LOG_FOLDER", t.TempDir())

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
The [LIMIT_IP] line is the jail's input, and the jail does not read the toggle.

Making the verdict independent of the actuator means the pass now has bans to
report on a host that is set not to ban — and the installer leaves a jail
watching that file whether or not PUI_ENABLE_FAIL2BAN is still true. Writing the
line anyway would start banning addresses on exactly the panels whose operator
turned banning off.
*/
func TestTheJailsInputIsWrittenOnlyWhereBanningIsOn(t *testing.T) {
	over := []service.IPLimitVerdict{{
		Email: "capped",
		Over:  []policy.Observation{{IP: "10.0.0.9", LastSeenUnixMilli: 1786398189000}},
	}}

	t.Run("enforcement off", func(t *testing.T) {
		t.Setenv("PUI_LOG_FOLDER", t.TempDir())
		NewCorePolicyJob().reportOverLimit(context.Background(), over, false)
		if lines := banLines(t); len(lines) != 0 {
			t.Fatalf("%d line(s) written to the jail's input on a host set not to ban:\n%s",
				len(lines), strings.Join(lines, "\n"))
		}
		if _, err := os.Stat(xray.GetIPLimitLogPath()); !os.IsNotExist(err) {
			t.Fatalf("the jail's input file was created on a host that never enforces, stat error: %v", err)
		}
	})

	t.Run("enforcement on", func(t *testing.T) {
		t.Setenv("PUI_LOG_FOLDER", t.TempDir())
		NewCorePolicyJob().reportOverLimit(context.Background(), over, true)
		if lines := banLines(t); len(lines) != 1 {
			t.Fatalf("%d line(s), want the one ban: the gate must control where the line goes, not whether it exists",
				len(lines))
		}
	})
}
