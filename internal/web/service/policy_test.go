package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/policy"
	"github.com/Arman2122/p-ui/internal/shaping"
	"github.com/Arman2122/p-ui/internal/shaping/shapetest"
	"github.com/Arman2122/p-ui/internal/web/runtime"
)

/*
A core built entirely out of what it declares.

The point of the whole layer is that a core earns speed ladders and IP limits by
implementing an interface, so the fixtures here implement exactly the interfaces
under test and nothing else — which is also what makes "core #5 costs nothing"
something these tests can actually check.
*/
type declaredCore struct {
	id       core.Kind
	kind     core.Kind
	selector core.Selector
	device   string
	keys     map[string]core.SubjectKey
	sessions []core.Session
	failWith error
}

func (c *declaredCore) Describe() core.Descriptor {
	return core.Descriptor{ID: c.id, TitleKey: "cores." + string(c.id) + ".title"}
}
func (c *declaredCore) Kinds() []core.Kind              { return []core.Kind{c.kind} }
func (c *declaredCore) Preflight(context.Context) error { return nil }

// shapingCore adds the identity half; a core without it is never asked for one.
type shapingCore struct{ *declaredCore }

func (c shapingCore) ShapingSelector(kind core.Kind) core.Selector {
	if kind != c.kind {
		return core.SelectorNone
	}
	return c.selector
}

func (c shapingCore) ShapingTargets(context.Context, core.Instance) (core.ShapingTarget, error) {
	return core.ShapingTarget{Device: c.device, Selector: c.selector, Keys: c.keys}, nil
}

// sessionCore adds the observation half.
type sessionCore struct{ *declaredCore }

func (c sessionCore) Sessions(context.Context) ([]core.Session, error) {
	if c.failWith != nil {
		return nil, c.failWith
	}
	return c.sessions, nil
}

// userCore adds a provisioner, which is what makes a client bounceable.
type userCore struct{ *declaredCore }

func (c userCore) AddUser(context.Context, core.Instance, core.User) error { return nil }
func (c userCore) RemoveUser(context.Context, core.Instance, string) error { return nil }
func (c userCore) Reconcile(context.Context, []core.Instance) error        { return nil }
func (c userCore) StopAll(context.Context) error                           { return nil }

// An L7 core: one socket set, so no kernel identity, but its sessions can be cut.
type proxyCore struct {
	*declaredCore
	sessionCore
	userCore
}

// An L3 core: a kernel identity per user, and a peer whose counters die with it.
type tunnelCore struct {
	*declaredCore
	shapingCore
	sessionCore
	userCore
}

func newProxyCore(base *declaredCore) *proxyCore {
	return &proxyCore{declaredCore: base, sessionCore: sessionCore{base}, userCore: userCore{base}}
}

func newTunnelCore(base *declaredCore) *tunnelCore {
	return &tunnelCore{
		declaredCore: base,
		shapingCore:  shapingCore{base},
		sessionCore:  sessionCore{base},
		userCore:     userCore{base},
	}
}

// installCores makes these the cores the join asks, and restores whatever was
// there so a shuffled run cannot leak a registry into another test.
func installCores(t *testing.T, made ...core.Core) {
	t.Helper()
	reg := core.NewRegistry()
	for _, c := range made {
		if err := reg.Register(c); err != nil {
			t.Fatalf("register %s: %v", c.Describe().ID, err)
		}
	}
	previous := runtime.GetManager()
	runtime.SetManager(runtime.NewManager(runtime.LocalDeps{Cores: reg}))
	t.Cleanup(func() { runtime.SetManager(previous) })
}

/*
installKernel swaps the shaping mechanism for the in-memory stand-in.

The devices are seeded because a core owns them: the panel installs a tree ON an
interface the wireguard engine created, and an absent one is a retryable state
the manager goes quiet about rather than an error.
*/
func installKernel(t *testing.T, devices ...string) *shapetest.Kernel {
	t.Helper()
	kernel := shapetest.New()
	kernel.AddLink(devices...)
	previous := shapingManager
	shapingManager = shaping.NewManager(kernel)
	t.Cleanup(func() { shapingManager = previous })
	return kernel
}

func seedPolicyInbound(t *testing.T, kind core.Kind, tag string, port int, emails ...string) *model.Inbound {
	t.Helper()
	clients := make([]map[string]any, 0, len(emails))
	for _, email := range emails {
		clients = append(clients, map[string]any{"email": email, "enable": true, "id": "11111111-1111-1111-1111-111111111111"})
	}
	blob, err := json.Marshal(map[string]any{"clients": clients})
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	inbound := &model.Inbound{
		UserId: 1, Tag: tag, Enable: true, Port: port,
		Protocol: model.Protocol(kind), Settings: string(blob),
	}
	if err := database.GetDB().Create(inbound).Error; err != nil {
		t.Fatalf("seed inbound %s: %v", tag, err)
	}
	for _, email := range emails {
		record := &model.ClientRecord{Email: email, Enable: true}
		if err := database.GetDB().Create(record).Error; err != nil {
			t.Fatalf("seed client %s: %v", email, err)
		}
		link := &model.ClientInbound{ClientId: record.Id, InboundId: inbound.Id}
		if err := database.GetDB().Create(link).Error; err != nil {
			t.Fatalf("link client %s: %v", email, err)
		}
	}
	return inbound
}

func setLimitIP(t *testing.T, email string, limit int) {
	t.Helper()
	if err := database.GetDB().Model(&model.ClientRecord{}).
		Where("email = ?", email).Update("limit_ip", limit).Error; err != nil {
		t.Fatalf("set limit_ip for %s: %v", email, err)
	}
}

func observe(email string, at time.Time, addresses ...string) []core.Session {
	out := make([]core.Session, 0, len(addresses))
	for i, address := range addresses {
		// Distinct seconds so oldest-first is deterministic rather than a tie.
		out = append(out, core.Session{
			Email:             email,
			Source:            netip.MustParseAddr(address),
			LastSeenUnixMilli: at.Add(time.Duration(i)*time.Second).Unix() * 1000,
		})
	}
	return out
}

func bannedAddresses(verdicts []IPLimitVerdict, email string) []string {
	for _, verdict := range verdicts {
		if verdict.Email != email {
			continue
		}
		out := make([]string, 0, len(verdict.Ban))
		for _, banned := range verdict.Ban {
			out = append(out, banned.IP)
		}
		sort.Strings(out)
		return out
	}
	return nil
}

/*
TestIPLimitKeepsTheNewestAddressesAndBansTheRest is the brief's own scenario,
driven through the real join rather than through the pure function it calls.
*/
func TestIPLimitKeepsTheNewestAddressesAndBansTheRest(t *testing.T) {
	initTestDB(t)
	svc := &PolicyService{}

	base := &declaredCore{id: "proxy", kind: "vless"}
	installCores(t, newProxyCore(base))
	seedPolicyInbound(t, "vless", "vless-cap", 45101, "sharer")
	setLimitIP(t, "sharer", 2)

	now := time.Now().Add(-time.Minute)
	base.sessions = observe("sharer", now, "203.0.113.1", "203.0.113.2", "203.0.113.3")

	verdicts, err := svc.EvaluateIPLimits(svc.ObserveSessions(context.Background()), true)
	if err != nil {
		t.Fatalf("EvaluateIPLimits: %v", err)
	}
	if got, want := bannedAddresses(verdicts, "sharer"), []string{"203.0.113.1"}; !slices.Equal(got, want) {
		t.Fatalf("banned %v, want the oldest address %v — the two newest keep the slots", got, want)
	}

	t.Run("a limit of zero is unlimited, never zero-allowed", func(t *testing.T) {
		setLimitIP(t, "sharer", 0)
		verdicts, err := svc.EvaluateIPLimits(svc.ObserveSessions(context.Background()), true)
		if err != nil {
			t.Fatalf("EvaluateIPLimits: %v", err)
		}
		if len(verdicts) != 0 {
			t.Fatalf("an unlimited client must never be banned, got %v", verdicts)
		}
		var row model.InboundClientIps
		if err := database.GetDB().Where("client_email = ?", "sharer").First(&row).Error; err != nil {
			t.Fatalf("read tracked ips: %v", err)
		}
		for _, want := range []string{"203.0.113.1", "203.0.113.2", "203.0.113.3"} {
			if !strings.Contains(row.Ips, want) {
				t.Errorf("every observed address must still be shown in the panel, %s missing from %s", want, row.Ips)
			}
		}
	})
}

/*
TestIPLimitReachesACoreThatHasNoOnlineApi is the capability that does not exist
today at all.

The retired job imported one core directly and returned early without it, so a
client on any other core had no limit and the UI never said so. This test cannot
be written against that job, which is what makes it the proof that observation
became core-agnostic.
*/
func TestIPLimitReachesACoreThatHasNoOnlineApi(t *testing.T) {
	initTestDB(t)
	svc := &PolicyService{}

	base := &declaredCore{
		id: "tunnel", kind: "wgkernel", selector: core.SelectorInnerIP, device: "pwg7",
	}
	installCores(t, newTunnelCore(base))
	seedPolicyInbound(t, "wgkernel", "wg-cap", 45102, "roamer")
	setLimitIP(t, "roamer", 2)

	// One key, three outer endpoints inside the window: a peer holds exactly one
	// endpoint at a time, so this roaming IS the key-sharing signal.
	base.sessions = observe("roamer", time.Now().Add(-time.Minute), "198.51.100.1", "198.51.100.2", "198.51.100.3")

	verdicts, err := svc.EvaluateIPLimits(svc.ObserveSessions(context.Background()), true)
	if err != nil {
		t.Fatalf("EvaluateIPLimits: %v", err)
	}
	if got, want := bannedAddresses(verdicts, "roamer"), []string{"198.51.100.1"}; !slices.Equal(got, want) {
		t.Fatalf("banned %v, want %v", got, want)
	}

	// Non-negotiable: a peer removed to satisfy a soft product rule loses its
	// byte counters, so this client is reported and never cut.
	for _, verdict := range verdicts {
		if verdict.Email == "roamer" && verdict.Bounce {
			t.Fatal("a client whose core gives it a kernel identity must never be peer-bounced: removing the peer zeroes the counters the panel bills from")
		}
	}
}

// TestAProxyClientIsStillBounced is the other half of that decision: without it,
// "never bounce" could pass by never bouncing anybody.
func TestAProxyClientIsStillBounced(t *testing.T) {
	initTestDB(t)
	svc := &PolicyService{}

	base := &declaredCore{id: "proxy", kind: "vless"}
	installCores(t, newProxyCore(base))
	seedPolicyInbound(t, "vless", "vless-bounce", 45103, "chatty")
	setLimitIP(t, "chatty", 1)
	base.sessions = observe("chatty", time.Now().Add(-time.Minute), "203.0.113.8", "203.0.113.9")

	verdicts, err := svc.EvaluateIPLimits(svc.ObserveSessions(context.Background()), true)
	if err != nil {
		t.Fatalf("EvaluateIPLimits: %v", err)
	}
	if len(verdicts) != 1 || !verdicts[0].Bounce {
		t.Fatalf("a client on a core with no kernel identity must be cut off, got %+v", verdicts)
	}
}

/*
TestOneCoreFailingDoesNotSilenceTheOthers is the per-core fail-open repair.

Today a single core's API being down skips the ENTIRE run, so every client on
every other core goes unenforced for as long as it stays down.
*/
func TestOneCoreFailingDoesNotSilenceTheOthers(t *testing.T) {
	initTestDB(t)
	svc := &PolicyService{}

	broken := &declaredCore{id: "broken", kind: "vmess", failWith: errors.New("api unavailable")}
	healthy := &declaredCore{id: "healthy", kind: "vless"}
	installCores(t, newProxyCore(broken), newProxyCore(healthy))

	seedPolicyInbound(t, "vmess", "broken-inbound", 45104, "onbroken")
	seedPolicyInbound(t, "vless", "healthy-inbound", 45105, "onhealthy")
	setLimitIP(t, "onbroken", 1)
	setLimitIP(t, "onhealthy", 1)
	broken.sessions = observe("onbroken", time.Now().Add(-time.Minute), "203.0.113.20", "203.0.113.21")
	healthy.sessions = observe("onhealthy", time.Now().Add(-time.Minute), "203.0.113.30", "203.0.113.31")

	scan := svc.ObserveSessions(context.Background())
	if !slices.Contains(scan.Silent, "broken") {
		t.Fatalf("the failing core must be reported as silent, got %v", scan.Silent)
	}
	verdicts, err := svc.EvaluateIPLimits(scan, true)
	if err != nil {
		t.Fatalf("EvaluateIPLimits: %v", err)
	}
	if got := bannedAddresses(verdicts, "onbroken"); got != nil {
		t.Errorf("a client whose core could not be asked must not be banned on a guess, got %v", got)
	}
	if got, want := bannedAddresses(verdicts, "onhealthy"), []string{"203.0.113.30"}; !slices.Equal(got, want) {
		t.Fatalf("the healthy core's client must still be enforced: banned %v, want %v", got, want)
	}
}

// TestAnObservationForADeletedClientDropsItsRow keeps #4963 fixed: a renamed or
// deleted client's email maps to no inbound and must not resurrect a ghost row.
func TestAnObservationForADeletedClientDropsItsRow(t *testing.T) {
	initTestDB(t)
	svc := &PolicyService{}

	base := &declaredCore{id: "proxy", kind: "vless"}
	installCores(t, newProxyCore(base))
	orphan := &model.InboundClientIps{ClientEmail: "renamed-away", Ips: `[{"ip":"203.0.113.5","timestamp":1}]`}
	if err := database.GetDB().Create(orphan).Error; err != nil {
		t.Fatalf("seed orphan row: %v", err)
	}
	base.sessions = observe("renamed-away", time.Now(), "203.0.113.5")

	if _, err := svc.EvaluateIPLimits(svc.ObserveSessions(context.Background()), true); err != nil {
		t.Fatalf("EvaluateIPLimits: %v", err)
	}
	var count int64
	if err := database.GetDB().Model(&model.InboundClientIps{}).
		Where("client_email = ?", "renamed-away").Count(&count).Error; err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != 0 {
		t.Fatalf("the orphaned tracking row should have been dropped, %d left", count)
	}
}

// TestAddressesAreCollectedWithoutALimit keeps #4800 fixed: the panel shows a
// client's addresses whether or not anyone capped them.
func TestAddressesAreCollectedWithoutALimit(t *testing.T) {
	initTestDB(t)
	svc := &PolicyService{}

	base := &declaredCore{id: "proxy", kind: "vless"}
	installCores(t, newProxyCore(base))
	seedPolicyInbound(t, "vless", "no-limit", 45106, "nolimit")
	base.sessions = observe("nolimit", time.Now(), "203.0.113.10")

	verdicts, err := svc.EvaluateIPLimits(svc.ObserveSessions(context.Background()), false)
	if err != nil {
		t.Fatalf("EvaluateIPLimits: %v", err)
	}
	if len(verdicts) != 0 {
		t.Fatalf("a collection-only pass must ban nobody, got %v", verdicts)
	}
	var row model.InboundClientIps
	if err := database.GetDB().Where("client_email = ?", "nolimit").First(&row).Error; err != nil {
		t.Fatalf("read tracked ips: %v", err)
	}
	if !strings.Contains(row.Ips, "203.0.113.10") {
		t.Fatalf("the observed address must be recorded for the panel, got %s", row.Ips)
	}
}

// TestPersistedAddressesStayInSeconds pins the storage unit. The blob crosses
// node syncs to panels running older code, so its unit is a wire contract.
func TestPersistedAddressesStayInSeconds(t *testing.T) {
	initTestDB(t)
	svc := &PolicyService{}

	base := &declaredCore{id: "proxy", kind: "vless"}
	installCores(t, newProxyCore(base))
	seedPolicyInbound(t, "vless", "unit-check", 45107, "unit")
	at := time.Now().Add(-time.Minute).Truncate(time.Second)
	base.sessions = observe("unit", at, "203.0.113.44")

	if _, err := svc.EvaluateIPLimits(svc.ObserveSessions(context.Background()), false); err != nil {
		t.Fatalf("EvaluateIPLimits: %v", err)
	}
	var row model.InboundClientIps
	if err := database.GetDB().Where("client_email = ?", "unit").First(&row).Error; err != nil {
		t.Fatalf("read tracked ips: %v", err)
	}
	var entries []model.ClientIpEntry
	if err := json.Unmarshal([]byte(row.Ips), &entries); err != nil {
		t.Fatalf("unmarshal %s: %v", row.Ips, err)
	}
	if len(entries) != 1 || entries[0].Timestamp != at.Unix() {
		t.Fatalf("persisted %v, want one entry stamped %d unix seconds", entries, at.Unix())
	}
}

// seedPlan stores a ladder and assigns it, returning the plan id.
func seedPlan(t *testing.T, name string, tiers []policy.Tier, emails ...string) int {
	t.Helper()
	blob, err := json.Marshal(SortTiers(tiers))
	if err != nil {
		t.Fatalf("marshal tiers: %v", err)
	}
	plan := &model.Policy{Name: name, Tiers: string(blob)}
	if err := database.GetDB().Create(plan).Error; err != nil {
		t.Fatalf("seed plan: %v", err)
	}
	for _, email := range emails {
		id := plan.Id
		if err := database.GetDB().Create(&model.ClientPolicy{Email: email, PolicyId: &id}).Error; err != nil {
			t.Fatalf("assign plan to %s: %v", email, err)
		}
	}
	return plan.Id
}

// theLadder is the brief's own example: unlimited, then 10 Mbps, then 2 Mbps.
var theLadder = []policy.Tier{
	{FromBytes: 0},
	{FromBytes: 50 << 30, UpBps: 10_000_000, DownBps: 10_000_000},
	{FromBytes: 100 << 30, UpBps: 2_000_000, DownBps: 2_000_000},
}

// shapedRates reads back what the want asks for, keyed by client.
func shapedRates(t *testing.T, wants []shaping.DeviceWant) map[string]shaping.Limits {
	t.Helper()
	out := map[string]shaping.Limits{}
	for _, want := range wants {
		for _, subject := range want.Subjects {
			out[subject.ID] = subject.Limits
		}
	}
	return out
}

func tunnelWithClients(t *testing.T, device string, emails ...string) (*declaredCore, *model.Inbound) {
	t.Helper()
	keys := make(map[string]core.SubjectKey, len(emails))
	for i, email := range emails {
		keys[email] = core.SubjectKey{Prefixes: []netip.Prefix{
			netip.MustParsePrefix(fmt.Sprintf("10.8.0.%d/32", i+10)),
		}}
	}
	base := &declaredCore{
		id: "tunnel", kind: "wgkernel", selector: core.SelectorInnerIP, device: device, keys: keys,
	}
	installCores(t, newTunnelCore(base))
	inbound := seedPolicyInbound(t, "wgkernel", "wg-"+device, 45200, emails...)
	for _, email := range emails {
		seedClientRow(t, email, inbound.Id, 0, 0, 0)
	}
	return base, inbound
}

/*
TestTheTierLadderIsEvaluatedFromCommittedUsage walks the brief's example.

Level-triggered: usage is re-read and the tier re-derived every pass, so the same
code answers a threshold crossing, a plan edit and a traffic reset.
*/
func TestTheTierLadderIsEvaluatedFromCommittedUsage(t *testing.T) {
	initTestDB(t)
	svc := &PolicyService{}
	tunnelWithClients(t, "pwg7", "climber")
	seedPlan(t, "the ladder", theLadder, "climber")

	setUsed := func(t *testing.T, up, down int64) {
		t.Helper()
		if err := database.GetDB().Model(&core.ClientTraffic{}).
			Where("email = ?", "climber").
			Updates(map[string]any{"up": up, "down": down}).Error; err != nil {
			t.Fatalf("set usage: %v", err)
		}
	}

	cases := []struct {
		name string
		used int64
		want shaping.Limits
	}{
		{"nothing used yet", 0, shaping.Limits{}},
		{"one byte short of the first threshold", 50<<30 - 1, shaping.Limits{}},
		{"exactly at the first threshold", 50 << 30, shaping.Limits{UpBps: 10_000_000, DownBps: 10_000_000}},
		{"most of the way to the second", 99 << 30, shaping.Limits{UpBps: 10_000_000, DownBps: 10_000_000}},
		{"exactly at the second threshold", 100 << 30, shaping.Limits{UpBps: 2_000_000, DownBps: 2_000_000}},
		{"far past the last rung", 10 << 40, shaping.Limits{UpBps: 2_000_000, DownBps: 2_000_000}},
		{"back to zero after a renewal", 0, shaping.Limits{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setUsed(t, tc.used/2, tc.used-tc.used/2)
			wants, err := svc.ShapingWants(context.Background())
			if err != nil {
				t.Fatalf("ShapingWants: %v", err)
			}
			got := shapedRates(t, wants)["climber"]
			if got != tc.want {
				t.Fatalf("at %d bytes used the client is limited to %+v, want %+v", tc.used, got, tc.want)
			}
			if tc.want == (shaping.Limits{}) && len(wants) != 1 {
				t.Fatalf("the device must still be converged when nobody on it is limited, got %d wants", len(wants))
			}
		})
	}
}

/*
TestATierReadsTheSameUsageAsAQuota is the depleted-but-unthrottled bug.

A client under the threshold locally but over it once a master's rows are folded
in must be throttled, because that is the number their quota is enforced against.
*/
func TestATierReadsTheSameUsageAsAQuota(t *testing.T) {
	initTestDB(t)
	svc := &PolicyService{}
	inboundSvc := &InboundService{}
	tunnelWithClients(t, "pwg7", "spread")
	seedPlan(t, "the ladder", theLadder, "spread")

	// Well under the first threshold on this panel alone.
	if err := database.GetDB().Model(&core.ClientTraffic{}).Where("email = ?", "spread").
		Updates(map[string]any{"up": int64(1 << 30), "down": int64(1 << 30)}).Error; err != nil {
		t.Fatalf("set local usage: %v", err)
	}
	wants, err := svc.ShapingWants(context.Background())
	if err != nil {
		t.Fatalf("ShapingWants: %v", err)
	}
	if got := shapedRates(t, wants)["spread"]; got != (shaping.Limits{}) {
		t.Fatalf("2 GiB of local usage is below the first rung, got %+v", got)
	}

	// A master reports the client's combined usage past the first threshold.
	if err := inboundSvc.AcceptGlobalTraffic("master-a", []*core.ClientTraffic{
		{Email: "spread", Up: 30 << 30, Down: 30 << 30},
	}); err != nil {
		t.Fatalf("AcceptGlobalTraffic: %v", err)
	}
	wants, err = svc.ShapingWants(context.Background())
	if err != nil {
		t.Fatalf("ShapingWants: %v", err)
	}
	want := shaping.Limits{UpBps: 10_000_000, DownBps: 10_000_000}
	if got := shapedRates(t, wants)["spread"]; got != want {
		t.Fatalf("cross-panel usage of 60 GiB is past the 50 GB rung, got %+v want %+v — the tier is reading a different number than the quota", got, want)
	}
}

/*
TestUsageArrivingFromARemoteNodeStillMovesTheTier proves the pass is not edge
triggered.

Bytes a master pushes never pass through the local delta writer, so a design that
recomputed on a traffic delta would pass every other test and fail this one.
*/
func TestUsageArrivingFromARemoteNodeStillMovesTheTier(t *testing.T) {
	initTestDB(t)
	svc := &PolicyService{}
	inboundSvc := &InboundService{}
	tunnelWithClients(t, "pwg7", "remote")
	seedPlan(t, "the ladder", theLadder, "remote")

	if err := inboundSvc.AcceptGlobalTraffic("master-a", []*core.ClientTraffic{
		{Email: "remote", Up: 60 << 30, Down: 60 << 30},
	}); err != nil {
		t.Fatalf("AcceptGlobalTraffic: %v", err)
	}
	wants, err := svc.ShapingWants(context.Background())
	if err != nil {
		t.Fatalf("ShapingWants: %v", err)
	}
	want := shaping.Limits{UpBps: 2_000_000, DownBps: 2_000_000}
	if got := shapedRates(t, wants)["remote"]; got != want {
		t.Fatalf("120 GiB reported by a master should be the last rung, got %+v want %+v", got, want)
	}
}

/*
TestATierCrossingCostsExactlyOneClassChange is the live-edit requirement.

A delete-and-re-add would satisfy "is the rate right" and would drop the client's
byte counters and their in-flight window with it.
*/
func TestATierCrossingCostsExactlyOneClassChange(t *testing.T) {
	initTestDB(t)
	svc := &PolicyService{}
	kernel := installKernel(t, "pwg7")
	tunnelWithClients(t, "pwg7", "crosser")
	seedPlan(t, "the ladder", theLadder, "crosser")

	setUsed := func(t *testing.T, used int64) {
		t.Helper()
		if err := database.GetDB().Model(&core.ClientTraffic{}).Where("email = ?", "crosser").
			Updates(map[string]any{"up": used, "down": int64(0)}).Error; err != nil {
			t.Fatalf("set usage: %v", err)
		}
	}

	setUsed(t, 50<<30)
	if err := svc.ConvergeShaping(context.Background()); err != nil {
		t.Fatalf("first converge: %v", err)
	}

	t.Run("a converged pass issues nothing at all", func(t *testing.T) {
		kernel.ResetOps()
		if err := svc.ConvergeShaping(context.Background()); err != nil {
			t.Fatalf("second converge: %v", err)
		}
		if kernel.Writes() != 0 {
			t.Fatalf("an unchanged want must issue zero operations, got %v", kernel.Ops())
		}
	})

	t.Run("crossing a threshold retunes and never rebuilds", func(t *testing.T) {
		kernel.ResetOps()
		setUsed(t, 100<<30)
		if err := svc.ConvergeShaping(context.Background()); err != nil {
			t.Fatalf("converge after the crossing: %v", err)
		}
		// One change per direction and nothing else. A ladder that limits both ways
		// is two independent trees, so two retunes is the whole cost of a crossing;
		// any add or delete here would take the client's counters and window with it.
		changed := map[string]int{}
		for _, op := range kernel.Ops() {
			if !strings.HasPrefix(op, "class~") {
				t.Errorf("a tier crossing must only retune, got %s", op)
				continue
			}
			changed[opDevice(op)]++
		}
		for _, device := range []string{"pwg7", "pifb7"} {
			if changed[device] != 1 {
				t.Errorf("%s saw %d class changes, want exactly 1: %v", device, changed[device], kernel.Ops())
			}
		}
	})

	t.Run("a traffic reset returns the client to full speed", func(t *testing.T) {
		kernel.ResetOps()
		setUsed(t, 0)
		if err := svc.ConvergeShaping(context.Background()); err != nil {
			t.Fatalf("converge after the reset: %v", err)
		}
		wants, err := svc.ShapingWants(context.Background())
		if err != nil {
			t.Fatalf("ShapingWants: %v", err)
		}
		if got := shapedRates(t, wants)["crosser"]; got != (shaping.Limits{}) {
			t.Fatalf("a renewed client must be unlimited again, got %+v", got)
		}
	})
}

/*
TestOnlyTheLimitedDirectionIsInstalled keeps the two trees independent.

An implementation that installs both whenever a client has any limit passes a
naive rate assertion and quietly builds a mirror device nobody needs.
*/
func TestOnlyTheLimitedDirectionIsInstalled(t *testing.T) {
	initTestDB(t)
	svc := &PolicyService{}
	tunnelWithClients(t, "pwg7", "downonly", "uponly")
	seedPlan(t, "download only", []policy.Tier{{FromBytes: 0, DownBps: 10_000_000}}, "downonly")
	seedPlan(t, "upload only", []policy.Tier{{FromBytes: 0, UpBps: 5_000_000}}, "uponly")

	kernel := installKernel(t, "pwg7")
	if err := svc.ConvergeShaping(context.Background()); err != nil {
		t.Fatalf("ConvergeShaping: %v", err)
	}

	// Exactly one shaped class on each tree: the download-capped client on the
	// device, the upload-capped one on the mirror, and neither on both.
	if got := shapedClassesOn(kernel.Ops(), "pwg7"); got != 1 {
		t.Errorf("the device carries %d shaped classes, want only the download-capped client: %v", got, kernel.Ops())
	}
	if got := shapedClassesOn(kernel.Ops(), "pifb7"); got != 1 {
		t.Errorf("the mirror carries %d shaped classes, want only the upload-capped client: %v", got, kernel.Ops())
	}
	if got := selectorsOn(kernel.Ops(), "pwg7", "dst_ip"); !slices.Equal(got, []string{"10.8.0.10/32"}) {
		t.Errorf("the download tree selects on %v, want only the download-capped client's address", got)
	}
	if got := selectorsOn(kernel.Ops(), "pifb7", "src_ip"); !slices.Equal(got, []string{"10.8.0.11/32"}) {
		t.Errorf("the mirror tree selects on %v, want only the upload-capped client's address", got)
	}
}

// opDevice reads the interface an op names. Matching on a substring would count
// an ingress redirect — which names both the device and the mirror — twice.
func opDevice(op string) string {
	fields := strings.Fields(op)
	if len(fields) < 3 {
		return ""
	}
	return fields[2]
}

// shapedClassesOn counts the classes installed for a client, so the explicitly
// created default class is not mistaken for one.
func shapedClassesOn(ops []string, device string) int {
	count := 0
	for _, op := range ops {
		if strings.HasPrefix(op, "class+") && opDevice(op) == device && !strings.Contains(op, "1:ffff") {
			count++
		}
	}
	return count
}

func selectorsOn(ops []string, device, match string) []string {
	var out []string
	for _, op := range ops {
		if !strings.HasPrefix(op, "filter+") || opDevice(op) != device {
			continue
		}
		fields := strings.Fields(op)
		for i, field := range fields {
			if field == match && i+1 < len(fields) {
				out = append(out, fields[i+1])
			}
		}
	}
	sort.Strings(out)
	return out
}

/*
TestADualStackClientGetsOneBudgetAndBothFamilies is docs §5.4's leak class.

A v4-only shaper silently does not limit v6 at all, and the two families must
share one class or the client is sold twice their rate.
*/
func TestADualStackClientGetsOneBudgetAndBothFamilies(t *testing.T) {
	initTestDB(t)
	svc := &PolicyService{}
	base, inbound := tunnelWithClients(t, "pwg7", "dual")
	base.keys["dual"] = core.SubjectKey{Prefixes: []netip.Prefix{
		netip.MustParsePrefix("10.8.0.10/32"),
		netip.MustParsePrefix("fd00::10/128"),
	}}
	_ = inbound
	seedPlan(t, "capped", []policy.Tier{{FromBytes: 0, DownBps: 10_000_000}}, "dual")

	kernel := installKernel(t, "pwg7")
	if err := svc.ConvergeShaping(context.Background()); err != nil {
		t.Fatalf("ConvergeShaping: %v", err)
	}

	classes, filters := 0, 0
	for _, op := range kernel.Ops() {
		switch {
		case strings.HasPrefix(op, "class+") && !strings.Contains(op, "1:ffff"):
			classes++
		case strings.HasPrefix(op, "filter+"):
			filters++
		}
	}
	if classes != 1 {
		t.Errorf("both families must share ONE budget, got %d classes in %v", classes, kernel.Ops())
	}
	if filters != 2 {
		t.Errorf("each family needs its own filter, got %d in %v", filters, kernel.Ops())
	}
}

/*
TestACoreThatDeclaresNothingCostsNothing is the non-negotiable, executed.

A core implementing neither new interface is skipped by both nil gates and needs
no edit anywhere in the rules, the mechanism or the join.
*/
func TestACoreThatDeclaresNothingCostsNothing(t *testing.T) {
	initTestDB(t)
	svc := &PolicyService{}

	silent := &declaredCore{id: "silent", kind: "mtproto"}
	installCores(t, silent)
	seedPolicyInbound(t, "mtproto", "mtproto-quiet", 45300, "quiet")
	setLimitIP(t, "quiet", 1)

	wants, err := svc.ShapingWants(context.Background())
	if err != nil {
		t.Fatalf("ShapingWants: %v", err)
	}
	if len(wants) != 0 {
		t.Fatalf("a core that declares no identity must be asked for none, got %v", wants)
	}
	scan := svc.ObserveSessions(context.Background())
	if len(scan.ByEmail) != 0 || len(scan.Silent) != 0 {
		t.Fatalf("a core that cannot name its sessions is not a failing core, got %+v", scan)
	}
	verdicts, err := svc.EvaluateIPLimits(scan, true)
	if err != nil {
		t.Fatalf("EvaluateIPLimits: %v", err)
	}
	if len(verdicts) != 0 {
		t.Fatalf("its clients must never be banned on no evidence, got %v", verdicts)
	}
}

// TestAnAssignmentToAMissingPlanFailsOpen: a row naming a plan that is gone
// leaves the client unshaped and visible, never on the strictest tier.
func TestAnAssignmentToAMissingPlanFailsOpen(t *testing.T) {
	initTestDB(t)
	svc := &PolicyService{}
	tunnelWithClients(t, "pwg7", "orphaned")
	id := seedPlan(t, "doomed", theLadder, "orphaned")

	if err := database.GetDB().Model(&core.ClientTraffic{}).Where("email = ?", "orphaned").
		Update("up", int64(200<<30)).Error; err != nil {
		t.Fatalf("set usage: %v", err)
	}
	if err := database.GetDB().Delete(&model.Policy{}, id).Error; err != nil {
		t.Fatalf("delete plan: %v", err)
	}

	var assignment model.ClientPolicy
	if err := database.GetDB().Where("email = ?", "orphaned").First(&assignment).Error; err != nil {
		t.Fatalf("the assignment must survive the plan, so the UI can report it: %v", err)
	}
	if assignment.PolicyId != nil {
		t.Fatalf("the foreign key must null the assignment rather than cascade it away, got %v", *assignment.PolicyId)
	}

	wants, err := svc.ShapingWants(context.Background())
	if err != nil {
		t.Fatalf("ShapingWants: %v", err)
	}
	if got := shapedRates(t, wants)["orphaned"]; got != (shaping.Limits{}) {
		t.Fatalf("an unresolvable plan must never throttle, got %+v", got)
	}
}
