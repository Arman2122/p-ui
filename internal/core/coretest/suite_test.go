package coretest

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

/*
The suite's own test.

Every entry in brokenAdapters is a real bug class taken from this repo's history
or from the counter semantics of a candidate daemon. If the suite stops catching
one of them it has decayed into a no-op, and without this test nobody would
notice until a core shipped with broken accounting.
*/

type defects struct {
	restartEveryReconcile bool
	billTheBaseline       bool
	clampBackwardsCounter bool
	alwaysNoop            bool
	overclaimShareLink    bool
	// trafficNeedsRunning models every real core: counters live inside the
	// daemon and report nothing once it is stopped.
	trafficNeedsRunning bool
	noopAddUser         bool
	keepsRemovedUser    bool
	forgetsOnline       bool
	quotaRejectsUnknown bool
	misattributeTraffic bool
	applyOnlyAdds       bool
	inventCredential    bool
	// The identity defects, driven by shapingCore. They are separated only
	// because a core that implements neither new interface must stay provable.
	shapeNothing        bool
	hideTheDevice       bool
	inventSelector      bool
	disagreeOnSelector  bool
	shapeAWholeSubnet   bool
	shareOnePrefix      bool
	shapeAStranger      bool
	zeroSessionLastSeen bool
	misattributeSession bool
	inventSessionLocal  bool
}

type fakeCore struct {
	d        defects
	starts   int
	running  bool
	counter  *core.Counter
	epoch    string
	readings map[string]int64
	prev     map[string]int64
	desired  []core.Instance
	served   map[string]bool
}

func newFake(d defects) *fakeCore {
	return &fakeCore{
		d:        d,
		counter:  core.NewCounter(),
		epoch:    "boot-1",
		readings: map[string]int64{},
		served:   map[string]bool{},
	}
}

func (f *fakeCore) Kinds() []core.Kind { return []core.Kind{"fake"} }

func (f *fakeCore) Describe() core.Descriptor {
	caps := core.Capabilities{
		UserHotAdd:    core.Yes(),
		PerUserStats:  core.Yes(),
		QuotaPushdown: core.Yes(),
		OnlineUsers:   core.Yes(),
		ShareLink:     core.No(),
	}
	if f.d.overclaimShareLink {
		caps.ShareLink = core.Yes()
	}
	return core.Descriptor{ID: "fake", TitleKey: "cores.fake.title", Caps: caps}
}

func (f *fakeCore) Preflight(context.Context) error { return nil }

func (f *fakeCore) ClientCredentials(core.Kind) []string {
	if f.d.inventCredential {
		return []string{"fakePassphrase"}
	}
	return []string{core.CredUUID}
}

func (f *fakeCore) Reconcile(_ context.Context, desired []core.Instance) error {
	changed := !slices.EqualFunc(f.desired, desired, func(a, b core.Instance) bool {
		return a.ID == b.ID && a.Tag == b.Tag && a.Port == b.Port && len(a.Users) == len(b.Users)
	})
	if changed || !f.running || f.d.restartEveryReconcile {
		f.starts++
		f.running = true
	}
	f.desired = desired
	f.served = map[string]bool{}
	for _, inst := range desired {
		for _, u := range inst.Users {
			f.served[u.Email] = true
		}
	}
	return nil
}

func (f *fakeCore) ApplyInstance(_ context.Context, inst core.Instance) error {
	if !f.d.applyOnlyAdds {
		f.served = map[string]bool{}
	}
	for _, u := range inst.Users {
		f.served[u.Email] = true
	}
	return nil
}

func (f *fakeCore) DropInstance(context.Context, core.Instance) error {
	f.served = map[string]bool{}
	return nil
}

func (f *fakeCore) AddUser(_ context.Context, _ core.Instance, user core.User) error {
	if f.d.noopAddUser {
		return nil
	}
	f.served[user.Email] = true
	return nil
}

func (f *fakeCore) RemoveUser(_ context.Context, _ core.Instance, email string) error {
	if f.d.keepsRemovedUser {
		return nil
	}
	delete(f.served, email)
	return nil
}

func (f *fakeCore) OnlineEmails(context.Context) ([]string, error) {
	if f.d.forgetsOnline {
		return nil, nil
	}
	var out []string
	for key := range f.readings {
		email, direction, _ := strings.Cut(key, "|")
		if direction == "up" {
			out = append(out, email)
		}
	}
	return out, nil
}

func (f *fakeCore) ResetQuota(_ context.Context, email string) error {
	if f.d.quotaRejectsUnknown && !f.served[email] {
		return errors.New("no such client")
	}
	return nil
}

func (f *fakeCore) StopAll(context.Context) error {
	f.running = false
	return nil
}

func (f *fakeCore) PlanChange(before, after core.Instance) core.Action {
	if f.d.alwaysNoop {
		return core.ActionNoop
	}
	if before.ID == after.ID && before.Tag == after.Tag && len(before.Users) == len(after.Users) {
		return core.ActionNoop
	}
	return core.ActionHotApply
}

func (f *fakeCore) CollectTraffic(context.Context) ([]core.TrafficDelta, error) {
	if f.d.trafficNeedsRunning && !f.running {
		return nil, nil
	}
	snapshot := make(map[string]int64, len(f.readings))
	for k, v := range f.readings {
		snapshot[k] = v
	}

	var deltas map[string]int64
	if f.d.billTheBaseline || f.d.clampBackwardsCounter {
		deltas = f.wrongDeltas(snapshot)
	} else {
		deltas = f.counter.Observe(f.epoch, snapshot)
	}

	byEmail := map[string]*core.TrafficDelta{}
	for key, value := range deltas {
		email, direction, _ := strings.Cut(key, "|")
		if f.d.misattributeTraffic {
			email = "someone-else@example.com"
		}
		d, ok := byEmail[email]
		if !ok {
			d = &core.TrafficDelta{Email: email, Tag: "fake-in"}
			byEmail[email] = d
		}
		if direction == "up" {
			d.Up += value
		} else {
			d.Down += value
		}
	}
	out := make([]core.TrafficDelta, 0, len(byEmail))
	for _, d := range byEmail {
		out = append(out, *d)
	}
	return out, nil
}

// wrongDeltas is the implementation the suite exists to reject. Two real bug
// classes: billing the opening read, and clamping a backwards counter to zero
// instead of re-baselining, which is the live bug in internal/mtproto.
func (f *fakeCore) wrongDeltas(snapshot map[string]int64) map[string]int64 {
	primed := f.prev != nil
	if f.prev == nil {
		f.prev = map[string]int64{}
	}
	out := map[string]int64{}
	for key, value := range snapshot {
		prev, seen := f.prev[key]
		f.prev[key] = value
		if !primed && !f.d.billTheBaseline {
			continue
		}
		if !seen {
			prev = 0
		}
		delta := value - prev
		if delta < 0 {
			if f.d.clampBackwardsCounter {
				continue
			}
			delta = value
		}
		if delta > 0 {
			out[key] = delta
		}
	}
	return out
}

func (f *fakeCore) feed(email string, up, down int64) {
	f.readings[email+"|up"] = up
	f.readings[email+"|down"] = down
}

func (f *fakeCore) restartSource() {
	f.epoch = f.epoch + "+"
	for k := range f.readings {
		f.readings[k] = 0
	}
}

/*
shapingCore is a fakeCore that also carries a kernel identity and reports
sessions.

A second type on purpose: Go interfaces are static, so one fake implementing
both would leave nothing proving the nil gates skip a core that implements
neither — which is the whole "a new core costs nothing" claim.
*/
type shapingCore struct {
	*fakeCore
	host     map[string]netip.Prefix
	sessions map[string]core.Session
}

func newShapingFake(d defects) *shapingCore {
	return &shapingCore{
		fakeCore: newFake(d),
		host:     map[string]netip.Prefix{},
		sessions: map[string]core.Session{},
	}
}

// Reconcile puts each user on the host at a fixed address. That placement is
// what hostSubjects reports, and what ShapingTargets has to agree with.
func (s *shapingCore) Reconcile(ctx context.Context, desired []core.Instance) error {
	for _, inst := range desired {
		for i, u := range inst.Users {
			if _, placed := s.host[u.Email]; !placed {
				s.host[u.Email] = netip.MustParsePrefix(fmt.Sprintf("10.0.0.%d/32", 11+i))
			}
		}
	}
	return s.fakeCore.Reconcile(ctx, desired)
}

func (s *shapingCore) hostSubjects() map[string][]string {
	out := make(map[string][]string, len(s.host))
	for email, prefix := range s.host {
		out[email] = []string{prefix.String()}
	}
	return out
}

func (s *shapingCore) ShapingSelector(core.Kind) core.Selector {
	switch {
	case s.d.shapeNothing:
		return core.SelectorNone
	case s.d.inventSelector:
		return core.Selector("everyThirdPacket")
	default:
		return core.SelectorInnerIP
	}
}

func (s *shapingCore) ShapingTargets(_ context.Context, inst core.Instance) (core.ShapingTarget, error) {
	target := core.ShapingTarget{
		Device:   "fake0",
		Selector: s.ShapingSelector(inst.Kind),
		Keys:     map[string]core.SubjectKey{},
	}
	if s.d.hideTheDevice {
		target.Device = ""
	}
	if s.d.disagreeOnSelector {
		target.Selector = core.SelectorFwmark
	}
	for _, u := range inst.Users {
		prefix, placed := s.host[u.Email]
		if !placed {
			continue
		}
		if s.d.shapeAWholeSubnet {
			prefix = netip.MustParsePrefix("10.0.0.0/24")
		}
		target.Keys[u.Email] = core.SubjectKey{Prefixes: []netip.Prefix{prefix}}
	}
	if s.d.shareOnePrefix && len(inst.Users) > 1 {
		target.Keys[inst.Users[1].Email] = target.Keys[inst.Users[0].Email]
	}
	if s.d.shapeAStranger {
		target.Keys["nobody@example.com"] = core.SubjectKey{Prefixes: []netip.Prefix{netip.MustParsePrefix("10.0.0.99/32")}}
	}
	return target, nil
}

func (s *shapingCore) feedSession(email, source string, lastSeenUnixMilli int64) {
	session := core.Session{
		Email:             email,
		Source:            netip.MustParseAddr(source),
		LastSeenUnixMilli: lastSeenUnixMilli,
	}
	// The address the HOST holds, so a healthy core proves the check does not fire
	// on every honest row — which is the half a defect test cannot show.
	if prefix, placed := s.host[email]; placed {
		session.Local = []netip.Addr{prefix.Addr()}
	}
	s.sessions[email] = session
}

func (s *shapingCore) Sessions(context.Context) ([]core.Session, error) {
	out := make([]core.Session, 0, len(s.sessions))
	for _, session := range s.sessions {
		if s.d.zeroSessionLastSeen {
			session.LastSeenUnixMilli = 0
		}
		if s.d.misattributeSession {
			session.Email = "someone-else@example.com"
		}
		if s.d.inventSessionLocal {
			session.Local = []netip.Addr{netip.MustParseAddr("10.0.0.99")}
		}
		out = append(out, session)
	}
	return out, nil
}

func shapingRig(s *shapingCore) Rig {
	rig := rigFor(s.fakeCore, func() (core.Core, error) { return s, nil })
	rig.HostSubjects = s.hostSubjects
	rig.FeedSession = s.feedSession
	return rig
}

func rigFor(f *fakeCore, asCore func() (core.Core, error)) Rig {
	return Rig{
		NewCore:  asCore,
		Instance: func(users int) core.Instance { return instanceWith(users) },
		Starts:   func() int { return f.starts },
		FeedTraffic: func(email string, up, down int64) {
			f.feed(email, up, down)
		},
		RestartSource: f.restartSource,
		ServedUsers: func() []string {
			out := make([]string, 0, len(f.served))
			for email := range f.served {
				out = append(out, email)
			}
			return out
		},
	}
}

func instanceWith(users int) core.Instance {
	inst := core.Instance{ID: 1, Kind: "fake", Tag: "fake-in", Listen: "127.0.0.1", Port: 8080, Enable: true}
	for i := range users {
		inst.Users = append(inst.Users, core.User{Email: string(rune('a'+i)) + "@example.com", Enable: true})
	}
	return inst
}

func TestConformingAdapterPasses(t *testing.T) {
	shared := newFake(defects{})
	failures := Check(rigFor(shared, func() (core.Core, error) { return shared, nil }))
	for _, f := range failures {
		t.Errorf("conforming adapter reported %s", f)
	}
}

func TestSuiteCatchesBrokenAdapters(t *testing.T) {
	tests := []struct {
		name          string
		defects       defects
		wantInvariant string
	}{
		{
			name:          "restarts the daemon on every reconcile",
			defects:       defects{restartEveryReconcile: true},
			wantInvariant: "reconcile/idempotent",
		},
		{
			name:          "bills the opening read",
			defects:       defects{billTheBaseline: true},
			wantInvariant: "traffic/baseline-first",
		},
		{
			name:          "loses every byte moved since a daemon restart",
			defects:       defects{clampBackwardsCounter: true},
			wantInvariant: "traffic/restart-counts-from-zero",
		},
		{
			name:          "claims a capability it does not implement",
			defects:       defects{overclaimShareLink: true},
			wantInvariant: "descriptor/capabilities-match",
		},
		{
			name:          "reports no change for a changed user set",
			defects:       defects{alwaysNoop: true},
			wantInvariant: "hotapply/detects-change",
		},
		{
			name:          "AddUser returns nil without reaching the daemon",
			defects:       defects{noopAddUser: true},
			wantInvariant: "adduser/reaches-the-daemon",
		},
		{
			name:          "RemoveUser leaves the client connectable",
			defects:       defects{keepsRemovedUser: true},
			wantInvariant: "removeuser/reaches-the-daemon",
		},
		{
			name:          "reports nobody online while a client is connected",
			defects:       defects{forgetsOnline: true},
			wantInvariant: "online/reports-a-connected-client",
		},
		{
			name:          "quota reset fails for a client the daemon does not know",
			defects:       defects{quotaRejectsUnknown: true},
			wantInvariant: "quota/reset-tolerates-unknown",
		},
		{
			name:          "bills the right total to the wrong client",
			defects:       defects{misattributeTraffic: true},
			wantInvariant: "traffic/attribution",
		},
		{
			name:          "ApplyInstance accumulates instead of converging",
			defects:       defects{applyOnlyAdds: true},
			wantInvariant: "apply/replaces-rather-than-adds",
		},
		{
			name:          "declares a credential field no form can render",
			defects:       defects{inventCredential: true},
			wantInvariant: "credentials/known-vocabulary",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			shared := newFake(tc.defects)
			failures := Check(rigFor(shared, func() (core.Core, error) { return shared, nil }))
			for _, f := range failures {
				if f.Invariant == tc.wantInvariant {
					return
				}
			}
			t.Errorf("suite did not catch %q: wanted invariant %q, got %v", tc.name, tc.wantInvariant, failures)
		})
	}
}

func TestConformingShapingAdapterPasses(t *testing.T) {
	shared := newShapingFake(defects{})
	for _, f := range Check(shapingRig(shared)) {
		t.Errorf("conforming shaping adapter reported %s", f)
	}
}

// TestACoreWithoutTheNewCapabilitiesIsUntouched pins both nil gates: a core that
// declares no identity passes with neither hook, so a new core costs nothing.
func TestACoreWithoutTheNewCapabilitiesIsUntouched(t *testing.T) {
	shared := newFake(defects{})
	rig := rigFor(shared, func() (core.Core, error) { return shared, nil })
	if rig.HostSubjects != nil || rig.FeedSession != nil {
		t.Fatal("the plain rig must supply neither identity hook, or the nil gates are not what this test drives")
	}
	for _, f := range Check(rig) {
		t.Errorf("a core implementing neither new capability reported %s", f)
	}
}

func TestSuiteCatchesBrokenIdentities(t *testing.T) {
	tests := []struct {
		name          string
		defects       defects
		wantInvariant string
	}{
		{
			name:          "implements ShapingHost while every kind shapes nothing",
			defects:       defects{shapeNothing: true},
			wantInvariant: "descriptor/capabilities-match",
		},
		{
			name:          "never names the device it shapes on",
			defects:       defects{hideTheDevice: true},
			wantInvariant: "shaping/names-the-device",
		},
		{
			name:          "declares a selector nothing can key on",
			defects:       defects{inventSelector: true},
			wantInvariant: "shaping/selector-vocabulary",
		},
		{
			name:          "shapes by one key while the form gates on another",
			defects:       defects{disagreeOnSelector: true},
			wantInvariant: "shaping/selector-agrees",
		},
		{
			name:          "keys a client by a whole subnet",
			defects:       defects{shapeAWholeSubnet: true},
			wantInvariant: "shaping/keys-are-host-prefixes",
		},
		{
			name:          "gives two clients the same address",
			defects:       defects{shareOnePrefix: true},
			wantInvariant: "shaping/keys-are-unique",
		},
		{
			name:          "keys a client the instance does not serve",
			defects:       defects{shapeAStranger: true},
			wantInvariant: "shaping/keys-match-users",
		},
		{
			name:          "reports a session with no last-seen time",
			defects:       defects{zeroSessionLastSeen: true},
			wantInvariant: "sessions/lastseen-advances",
		},
		{
			name:          "attributes a session to the wrong client",
			defects:       defects{misattributeSession: true},
			wantInvariant: "sessions/reports-what-the-host-sees",
		},
		{
			name:          "reports an in-tunnel address the device does not answer to",
			defects:       defects{inventSessionLocal: true},
			wantInvariant: "sessions/local-is-the-hosts",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			shared := newShapingFake(tc.defects)
			failures := Check(shapingRig(shared))
			for _, f := range failures {
				if f.Invariant == tc.wantInvariant {
					return
				}
			}
			t.Errorf("suite did not catch %q: wanted invariant %q, got %v", tc.name, tc.wantInvariant, failures)
		})
	}
}

// TestIdentityRigWithoutTheHostReportsUnverifiable is the ServedUsers rule for
// the new checks: what the adapter says is worth nothing without the host's word.
func TestIdentityRigWithoutTheHostReportsUnverifiable(t *testing.T) {
	shared := newShapingFake(defects{})
	rig := shapingRig(shared)
	rig.HostSubjects = nil
	rig.FeedSession = nil

	var got []string
	for _, f := range Check(rig) {
		got = append(got, f.Invariant)
	}
	for _, want := range []string{"shaping/verifiable", "sessions/verifiable"} {
		if !slices.Contains(got, want) {
			t.Errorf("a rig that cannot verify %s must say so rather than pass silently; got %v", want, got)
		}
	}
}

// TestTrafficIsCheckedWhileTheCoreRuns pins the order of Check. Every real core
// keeps its counters inside the daemon, so a suite that stopped the core before
// the accounting checks would report zero bytes and blame the adapter.
func TestTrafficIsCheckedWhileTheCoreRuns(t *testing.T) {
	shared := newFake(defects{trafficNeedsRunning: true})
	for _, f := range Check(rigFor(shared, func() (core.Core, error) { return shared, nil })) {
		t.Errorf("a conforming core whose counters only exist while its daemon runs must still pass: %s", f)
	}
}

func TestRigWithoutCountersReportsUnverifiable(t *testing.T) {
	shared := newFake(defects{})
	rig := rigFor(shared, func() (core.Core, error) { return shared, nil })
	rig.Starts = nil
	rig.RestartSource = nil
	rig.ServedUsers = nil

	var got []string
	for _, f := range Check(rig) {
		got = append(got, f.Invariant)
	}
	for _, want := range []string{"reconcile/idempotent", "traffic/restart-verifiable", "users/verifiable"} {
		if !slices.Contains(got, want) {
			t.Errorf("a rig that cannot verify %s must say so rather than pass silently; got %v", want, got)
		}
	}
}
