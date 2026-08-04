package coretest

import (
	"context"
	"errors"
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
