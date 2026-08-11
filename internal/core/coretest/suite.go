// Package coretest is the conformance suite every core adapter must pass.
package coretest

import (
	"context"
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
)

/*
One suite, run by every core, so a new core arrives with its correctness argument
already written.

Check is a pure function returning failures rather than reporting to *testing.T,
for one reason: a conformance suite that cannot itself be tested decays into an
elaborate no-op and nobody notices for a year. suite_test.go runs Check against a
family of deliberately broken adapters and asserts each one is caught.

Gating comes from the core's declared capabilities and nothing else. There is no
SkipX option, because dropping a capability is a visible public-API change that
shows up in review, while setting a bool in a test file is not.
*/

// Rig is what a core supplies so the suite can drive it. Nothing here takes a
// *testing.T: the suite must stay callable outside a test to be testable itself.
type Rig struct {
	// NewCore builds a fresh, unstarted core. Required.
	NewCore func() (core.Core, error)

	// Instance builds valid desired state carrying n users. Required.
	Instance func(users int) core.Instance

	// Starts reports how many times the underlying daemon has been started.
	// Without it, hot-apply cannot be told from restart and those checks are
	// reported as unverifiable rather than silently skipped.
	Starts func() int

	// FeedTraffic sets the cumulative bytes the core's source will report.
	// Required when the core declares PerUserStats.
	FeedTraffic func(email string, up, down int64)

	// RestartSource simulates the daemon restarting with its counters at zero.
	RestartSource func()

	// ServedUsers reports the emails the daemon is serving, read from the daemon
	// not the adapter. Without it, a no-op AddUser looks like a working one.
	ServedUsers func() []string

	// HostSubjects reports the kernel identity per email as the HOST holds it, read
	// off the device. Without it, a map the adapter invented cannot be told apart.
	HostSubjects func() map[string][]string

	// FeedSession makes one session observable, so Sessions can be checked against
	// something the suite controls rather than whatever happens to be live.
	FeedSession func(email, source string, lastSeenUnixMilli int64)
}

// Subject is the client the traffic and provisioning checks operate on. It is
// fixed so a rig can pre-register it if its daemon needs users declared.
const Subject = "a@example.com"

// SessionSource is the address the session checks drive. RFC 5737 documentation
// space, so it can never collide with a real endpoint a rig's daemon reports.
const SessionSource = "203.0.113.7"

// Failure is one violated invariant.
type Failure struct {
	Invariant string
	Detail    string
}

func (f Failure) String() string { return f.Invariant + ": " + f.Detail }

type report struct {
	failures []Failure
}

func (r *report) fail(invariant, format string, args ...any) {
	r.failures = append(r.failures, Failure{Invariant: invariant, Detail: fmt.Sprintf(format, args...)})
}

// Check runs every invariant applicable to the core the rig builds and returns
// the failures. An empty result means the core conforms.
func Check(rig Rig) []Failure {
	r := &report{}
	if rig.NewCore == nil || rig.Instance == nil {
		r.fail("rig/complete", "NewCore and Instance are required; a rig missing them cannot exercise anything")
		return r.failures
	}

	c, err := rig.NewCore()
	if err != nil {
		r.fail("rig/newcore", "NewCore returned an error: %v", err)
		return r.failures
	}
	if c == nil {
		r.fail("rig/newcore", "NewCore returned nil")
		return r.failures
	}
	bound := core.Bind(c)

	checkIdentity(r, c)
	checkDescriptor(r, bound)
	checkCredentials(r, bound)
	checkRegistry(r, rig)
	checkSupervisor(r, rig, bound)
	checkHotApply(r, rig, bound)
	checkTraffic(r, rig, bound)
	checkOnline(r, bound)
	// Both run here and not later: checkInstanceApply ends in DropInstance, which
	// takes the device with it, and neither identity is observable without one.
	checkShaping(r, rig, bound)
	checkSessions(r, rig, bound)
	checkUsers(r, rig, bound)
	checkInstanceApply(r, rig, bound)
	checkQuota(r, bound)
	checkTeardown(r, bound)
	return r.failures
}

func checkIdentity(r *report, c core.Core) {
	kinds := c.Kinds()
	if len(kinds) == 0 {
		r.fail("kinds/non-empty", "Kinds() is empty; the registry has nothing to key this core on")
		return
	}
	seen := map[core.Kind]bool{}
	for _, k := range kinds {
		if k == "" {
			r.fail("kinds/no-empty-value", "Kinds() contains an empty kind")
		}
		if seen[k] {
			r.fail("kinds/no-duplicates", "Kinds() lists %q more than once", k)
		}
		seen[k] = true
	}
	if c.Describe().ID == "" {
		r.fail("descriptor/id-set", "Describe().ID is empty; it names the core in logs, the API and the UI")
	}
}

func checkDescriptor(r *report, bound *core.Bound) {
	for _, problem := range bound.DeclaredMatchesImplemented() {
		r.fail("descriptor/capabilities-match", "%s", problem)
	}
}

// checkCredentials holds a declaring core to the closed vocabulary: a name
// outside it renders as nothing, so that field can never be filled in.
func checkCredentials(r *report, bound *core.Bound) {
	if bound.Creds == nil {
		return
	}
	for _, kind := range bound.Core.Kinds() {
		for _, name := range bound.Creds.ClientCredentials(kind) {
			if !core.IsClientCredential(name) {
				r.fail("credentials/known-vocabulary", "kind %q declares credential %q, which is outside the vocabulary in internal/core/credentials.go; no form renders it, so the operator has no way to set it", kind, name)
			}
		}
	}
}

func checkRegistry(r *report, rig Rig) {
	first, err := rig.NewCore()
	if err != nil {
		r.fail("rig/newcore", "NewCore returned an error on reuse: %v", err)
		return
	}
	reg := core.NewRegistry()
	if err := reg.Register(first); err != nil {
		r.fail("registry/accepts", "a conforming core must register: %v", err)
		return
	}
	for _, kind := range first.Kinds() {
		if _, ok := reg.For(kind); !ok {
			r.fail("registry/resolves", "kind %q is not resolvable after registration", kind)
		}
	}
	second, err := rig.NewCore()
	if err != nil {
		r.fail("rig/newcore", "NewCore returned an error on reuse: %v", err)
		return
	}
	if err := reg.Register(second); err == nil {
		r.fail("registry/rejects-duplicate", "registering the same kinds twice succeeded; a duplicate must be an error, not a silent overwrite")
	}
}

func checkSupervisor(r *report, rig Rig, bound *core.Bound) {
	if bound.Supervise == nil {
		r.fail("supervisor/required", "core does not implement Supervisor; without Reconcile it cannot converge after a crash, so every panel restart becomes a correctness event")
		return
	}
	ctx := context.Background()
	desired := []core.Instance{rig.Instance(2)}

	if err := bound.Supervise.Reconcile(ctx, desired); err != nil {
		r.fail("reconcile/succeeds", "first Reconcile failed: %v", err)
		return
	}
	if rig.Starts == nil {
		r.fail("reconcile/idempotent", "unverifiable: the rig supplies no Starts counter, so a core that restarts its daemon on every reconcile cannot be detected")
	} else {
		afterFirst := rig.Starts()
		if afterFirst == 0 {
			r.fail("reconcile/starts", "Reconcile did not start the daemon")
		}
		if err := bound.Supervise.Reconcile(ctx, desired); err != nil {
			r.fail("reconcile/succeeds", "second Reconcile failed: %v", err)
			return
		}
		if got := rig.Starts(); got != afterFirst {
			r.fail("reconcile/idempotent", "reconciling unchanged state restarted the daemon (%d starts, was %d); every reconcile would drop live connections", got, afterFirst)
		}
	}
}

// checkOnline runs after the traffic checks, whose last reading has the subject
// connected. Answering from that scrape is fine; answering nothing is not.
func checkOnline(r *report, bound *core.Bound) {
	if bound.Online == nil {
		return
	}
	emails, err := bound.Online.OnlineEmails(context.Background())
	if err != nil {
		r.fail("online/succeeds", "OnlineEmails failed: %v", err)
		return
	}
	if !slices.Contains(emails, Subject) {
		r.fail("online/reports-a-connected-client", "OnlineEmails returned %v, without %q, which has just moved bytes over a live connection; a core declaring OnlineUsers and returning nothing reads as everyone being offline", emails, Subject)
	}
}

/*
checkShaping holds a shaping core to an identity the HOST agrees with.

Read back from Rig.HostSubjects rather than trusted, for the reason ServedUsers
exists: an adapter returning a plausible map it invented is indistinguishable
from one that works. The kernel is the authority — it moves a shared address to
the later peer — so an identity that disagrees with it shapes the wrong client.
*/
func checkShaping(r *report, rig Rig, bound *core.Bound) {
	if bound.Shape == nil {
		return
	}
	for _, kind := range bound.Core.Kinds() {
		switch selector := bound.Shape.ShapingSelector(kind); selector {
		case core.SelectorNone, core.SelectorInnerIP, core.SelectorFwmark:
		default:
			r.fail("shaping/selector-vocabulary", "kind %q declares selector %q, which is outside the vocabulary in internal/core/caps.go; nothing can key on it, and an unknown selector must read as \"cannot shape\" rather than \"probably fine\"", kind, selector)
		}
	}
	if rig.HostSubjects == nil {
		r.fail("shaping/verifiable", "unverifiable: core implements ShapingHost but the rig supplies no HostSubjects, so a map the adapter invented cannot be told from what the host holds")
		return
	}
	ctx := context.Background()
	// The traffic checks end with the daemon restarted and its state wiped, and an
	// identity is only observable while the instance is actually being served.
	inst := rig.Instance(2)
	if bound.Supervise != nil {
		if err := bound.Supervise.Reconcile(ctx, []core.Instance{inst}); err != nil {
			r.fail("shaping/reconcile", "reconciling before the identity checks failed: %v", err)
			return
		}
	}

	target, err := bound.Shape.ShapingTargets(ctx, inst)
	if err != nil {
		r.fail("shaping/targets-succeed", "ShapingTargets failed: %v", err)
		return
	}
	if target.Device == "" {
		r.fail("shaping/names-the-device", "ShapingTargets named no device for an instance this core is serving; an empty Device means \"not hosting it right now\", so a core that always answers that is never shaped and nothing ever fails")
		return
	}
	if declared := bound.Shape.ShapingSelector(inst.Kind); target.Selector != declared {
		r.fail("shaping/selector-agrees", "kind %q declares selector %q but its instance reports %q; the client form gates on the first and the shaper keys on the second, so a limit is offered that nothing enforces", inst.Kind, declared, target.Selector)
	}
	compareSubjects(r, inst, target, rig.HostSubjects())
}

// compareSubjects checks the target against the instance it belongs to and
// against the host, which is the only source that cannot be invented.
func compareSubjects(r *report, inst core.Instance, target core.ShapingTarget, host map[string][]string) {
	served := make(map[string]bool, len(inst.Users))
	for _, u := range inst.Users {
		served[u.Email] = true
	}

	// Sorted, so a real adapter's failure reads the same on every run: which of two
	// clients sharing an address is named "first" is otherwise map order.
	owner := map[netip.Prefix]string{}
	got := map[string][]string{}
	for _, email := range slices.Sorted(maps.Keys(target.Keys)) {
		key := target.Keys[email]
		if !served[email] {
			r.fail("shaping/keys-match-users", "the target keys %q, who is not a user of instance %d; a stale map shapes a deleted client's successor", email, inst.ID)
		}
		if len(key.Prefixes) == 0 {
			r.fail("shaping/keys-are-host-prefixes", "%q is keyed by no prefix at all; a client the core cannot distinguish belongs outside Keys, not inside it with an empty identity", email)
		}
		for _, prefix := range key.Prefixes {
			switch other, taken := owner[prefix]; {
			case !prefix.IsSingleIP():
				r.fail("shaping/keys-are-host-prefixes", "%q is keyed by %s, which is not a single address; a wider prefix shapes everyone inside it as this one client", email, prefix)
			case taken:
				r.fail("shaping/keys-are-unique", "%s is claimed by both %q and %q; one of them is shaped as the other, which is the one failure a customer cannot detect for themselves", prefix, other, email)
			default:
				owner[prefix] = email
				got[email] = append(got[email], prefix.String())
			}
		}
	}

	want := map[string][]string{}
	for _, email := range slices.Sorted(maps.Keys(host)) {
		if !served[email] {
			continue
		}
		for _, raw := range host[email] {
			prefix, err := netip.ParsePrefix(raw)
			if err != nil {
				r.fail("shaping/verifiable", "the rig reports %q as %q's identity on the host, which is not a prefix", raw, email)
				continue
			}
			if prefix.IsSingleIP() {
				want[email] = append(want[email], prefix.String())
			}
		}
	}
	for _, set := range []map[string][]string{got, want} {
		for _, prefixes := range set {
			slices.Sort(prefixes)
		}
	}

	for _, email := range slices.Sorted(maps.Keys(want)) {
		if !slices.Equal(got[email], want[email]) {
			r.fail("shaping/verifiable", "the host holds %v for %q and the core reports %v; a limit is attached to what the core says, so the two disagreeing means somebody else is shaped", want[email], email, got[email])
		}
	}
	for _, email := range slices.Sorted(maps.Keys(got)) {
		if _, known := want[email]; !known {
			r.fail("shaping/verifiable", "the core reports %v for %q, which the host holds no address for; an invented key shapes an address nobody answers to", got[email], email)
		}
	}
}

// checkSessions drives one session the rig controls, because a core answering
// with whatever is live cannot be told from one answering a plausible constant.
func checkSessions(r *report, rig Rig, bound *core.Bound) {
	if bound.Sessions == nil {
		return
	}
	if rig.FeedSession == nil {
		r.fail("sessions/verifiable", "unverifiable: core implements SessionReporter but the rig supplies no FeedSession, so nothing it reports can be checked against a session the suite put there")
		return
	}
	ctx := context.Background()
	if bound.Supervise != nil {
		if err := bound.Supervise.Reconcile(ctx, []core.Instance{rig.Instance(2)}); err != nil {
			r.fail("sessions/reconcile", "reconciling before the session checks failed: %v", err)
			return
		}
	}
	// Whole seconds, and inside the last minute: a core whose source keeps seconds
	// round-trips these exactly, and a liveness window still counts them live.
	seen := time.Now().Add(-time.Minute).Truncate(time.Second)
	later := seen.Add(30 * time.Second)

	rig.FeedSession(Subject, SessionSource, seen.UnixMilli())
	first := subjectSession(r, bound)
	if first == nil {
		return
	}
	if got := first.LastSeenUnixMilli; got != seen.UnixMilli() {
		r.fail("sessions/lastseen-advances", "the session was last seen at %d and Sessions reports %d; the value is what the ban dedup compares, so one the core made up bans a client that never reconnected", seen.UnixMilli(), got)
		return
	}

	rig.FeedSession(Subject, SessionSource, later.UnixMilli())
	second := subjectSession(r, bound)
	if second == nil {
		return
	}
	if got := second.LastSeenUnixMilli; got != later.UnixMilli() {
		r.fail("sessions/lastseen-advances", "the session was seen again at %d and Sessions still reports %d; a core that cannot advance it makes every re-ban fire forever or never", later.UnixMilli(), got)
	}
}

// subjectSession returns the reported session for the subject at SessionSource,
// or nil after reporting why the one the rig put on the wire is not there.
func subjectSession(r *report, bound *core.Bound) *core.Session {
	got, err := bound.Sessions.Sessions(context.Background())
	if err != nil {
		r.fail("sessions/succeeds", "Sessions failed: %v", err)
		return nil
	}
	for i, s := range got {
		if s.Email == Subject && s.Source.String() == SessionSource {
			return &got[i]
		}
	}
	r.fail("sessions/reports-what-the-host-sees", "Sessions returned %v, without %q from %s, which the rig has just put on the wire; an unreported session is a cap nobody is held to, and one attributed to the wrong client bans somebody who was inside it", got, Subject, SessionSource)
	return nil
}

// checkUsers exercises live provisioning against what the daemon serves: an
// AddUser that returns nil without touching it is the no-op caps.go warns about.
func checkUsers(r *report, rig Rig, bound *core.Bound) {
	if bound.Users == nil {
		return
	}
	if rig.ServedUsers == nil {
		r.fail("users/verifiable", "unverifiable: core implements UserProvisioner but the rig supplies no ServedUsers, so an AddUser that does nothing cannot be told from one that works")
		return
	}
	if bound.Supervise == nil {
		return
	}
	ctx := context.Background()
	base := rig.Instance(1)
	pair := rig.Instance(2)
	if len(pair.Users) < 2 {
		r.fail("rig/instance", "Instance(2) returned %d users; the provisioning checks need two distinguishable clients", len(pair.Users))
		return
	}
	extra := pair.Users[1]

	if err := bound.Supervise.Reconcile(ctx, []core.Instance{base}); err != nil {
		r.fail("users/reconcile", "reconciling before the provisioning checks failed: %v", err)
		return
	}
	/*
		A core that provisions one user is handed one user, and a removal is
		handed none, because that is what the runtime actually passes it.

		Handing over a fully projected instance here would let a core read
		inst.Users, pass this suite, and then revoke everyone the trimmed
		instance omits — the suite teaching the opposite of the contract.
	*/
	addInst, dropInst := base, pair
	if bound.UserSet == nil {
		addInst.Users = []core.User{extra}
		dropInst.Users = nil
	}

	if err := bound.Users.AddUser(ctx, addInst, extra); err != nil {
		r.fail("adduser/succeeds", "AddUser failed: %v", err)
		return
	}
	if served := rig.ServedUsers(); !slices.Contains(served, extra.Email) {
		r.fail("adduser/reaches-the-daemon", "after AddUser the daemon serves %v, without %q; the user cannot connect and nothing failed", served, extra.Email)
	}

	if err := bound.Users.RemoveUser(ctx, dropInst, extra.Email); err != nil {
		r.fail("removeuser/succeeds", "RemoveUser failed: %v", err)
		return
	}
	if served := rig.ServedUsers(); slices.Contains(served, extra.Email) {
		r.fail("removeuser/reaches-the-daemon", "after RemoveUser the daemon still serves %v; a revoked client keeps connecting, which is how a depleted user keeps spending", served)
	}
}

// checkInstanceApply drives the per-instance path. It is the one every edit in
// the panel takes, so an implementation that only ever adds - never replaces -
// leaves a revoked client connectable while every test of the set-based path
// still passes.
func checkInstanceApply(r *report, rig Rig, bound *core.Bound) {
	if bound.Apply == nil {
		return
	}
	if rig.ServedUsers == nil {
		r.fail("apply/verifiable", "unverifiable: core implements InstanceApplier but the rig supplies no ServedUsers, so an ApplyInstance that does nothing cannot be told from one that works")
		return
	}
	ctx := context.Background()
	pair := rig.Instance(2)
	if len(pair.Users) < 2 {
		return
	}

	if err := bound.Apply.ApplyInstance(ctx, pair); err != nil {
		r.fail("apply/succeeds", "ApplyInstance failed: %v", err)
		return
	}
	for _, u := range pair.Users {
		if !slices.Contains(rig.ServedUsers(), u.Email) {
			r.fail("apply/reaches-the-daemon", "after ApplyInstance the daemon serves %v, without %q", rig.ServedUsers(), u.Email)
			return
		}
	}

	// Applying a smaller set must remove what it left out. An apply that only
	// ever adds keeps a revoked client connecting.
	if err := bound.Apply.ApplyInstance(ctx, rig.Instance(1)); err != nil {
		r.fail("apply/succeeds", "ApplyInstance with fewer users failed: %v", err)
		return
	}
	if served := rig.ServedUsers(); slices.Contains(served, pair.Users[1].Email) {
		r.fail("apply/replaces-rather-than-adds", "after applying a smaller user set the daemon still serves %v; ApplyInstance must converge on what it is given, not accumulate", served)
	}

	if err := bound.Apply.DropInstance(ctx, rig.Instance(1)); err != nil {
		r.fail("drop/succeeds", "DropInstance failed: %v", err)
	}
}

// checkQuota asserts pushdown does not error, including for an unknown email:
// a reset runs on renewal, where an error is a paid-up client that stays blocked.
func checkQuota(r *report, bound *core.Bound) {
	if bound.Quota == nil {
		return
	}
	ctx := context.Background()
	if err := bound.Quota.ResetQuota(ctx, Subject); err != nil {
		r.fail("quota/reset-succeeds", "ResetQuota(%q) failed: %v", Subject, err)
	}
	if err := bound.Quota.ResetQuota(ctx, "nobody@example.com"); err != nil {
		r.fail("quota/reset-tolerates-unknown", "ResetQuota for an unknown client failed: %v; the panel resets by email without knowing which core holds it", err)
	}
}

// checkTeardown runs last. A core's counters live inside its daemon, so stopping
// it earlier makes every traffic invariant unobservable instead of failing.
func checkTeardown(r *report, bound *core.Bound) {
	if bound.Supervise == nil {
		return
	}
	ctx := context.Background()
	if err := bound.Supervise.StopAll(ctx); err != nil {
		r.fail("stopall/succeeds", "StopAll failed: %v", err)
	}
	if err := bound.Supervise.StopAll(ctx); err != nil {
		r.fail("stopall/idempotent", "StopAll is not safe to call twice: %v", err)
	}
}

func checkHotApply(r *report, rig Rig, bound *core.Bound) {
	if bound.HotApply == nil {
		return
	}
	inst := rig.Instance(1)
	if got := bound.HotApply.PlanChange(inst, inst); got != core.ActionNoop {
		r.fail("hotapply/noop-on-identical", "PlanChange on identical state returned %s, want noop; otherwise a no-op save restarts the daemon", got)
	}
	withUser := rig.Instance(2)
	if got := bound.HotApply.PlanChange(inst, withUser); got == core.ActionNoop {
		r.fail("hotapply/detects-change", "PlanChange reported noop for a changed user set; the change would never be applied")
	}
}

// checkTraffic runs while the instances reconciled above are still up, which is
// the only state in which a daemon-backed counter reports anything at all.
func checkTraffic(r *report, rig Rig, bound *core.Bound) {
	declared := core.Known(bound.Core.Describe().Caps.PerUserStats)
	if bound.Traffic == nil {
		if declared {
			r.fail("descriptor/capabilities-match", "PerUserStats is declared but TrafficSource is not implemented")
		}
		return
	}
	if rig.FeedTraffic == nil {
		r.fail("traffic/verifiable", "unverifiable: core implements TrafficSource but the rig supplies no FeedTraffic, so no accounting invariant can be checked")
		return
	}
	ctx := context.Background()

	rig.FeedTraffic(Subject, 1_000, 2_000)
	first, err := bound.Traffic.CollectTraffic(ctx)
	if err != nil {
		r.fail("traffic/collects", "first CollectTraffic failed: %v", err)
		return
	}
	if total(first) != 0 {
		r.fail("traffic/baseline-first", "first CollectTraffic reported %d bytes; the opening read only establishes baselines, or traffic already billed by a previous panel process is billed again", total(first))
	}

	rig.FeedTraffic(Subject, 1_500, 2_500)
	second, err := bound.Traffic.CollectTraffic(ctx)
	if err != nil {
		r.fail("traffic/collects", "second CollectTraffic failed: %v", err)
		return
	}
	if got := total(second); got != 1_000 {
		r.fail("traffic/delta-after-baseline", "reported %d bytes, want 1000; deltas must be the difference since the previous read", got)
	}
	if got := billedTo(second, Subject); got != 1_000 {
		r.fail("traffic/attribution", "%d of 1000 bytes were billed to %q, the rest to some other client; client_traffics is email-keyed and shared, so a misattributed byte is charged to a user who never sent it", got, Subject)
	}
	for _, d := range second {
		if d.Up < 0 || d.Down < 0 {
			r.fail("traffic/never-negative", "negative delta for %q (up=%d down=%d)", d.Email, d.Up, d.Down)
		}
	}

	if rig.RestartSource == nil {
		r.fail("traffic/restart-verifiable", "unverifiable: the rig supplies no RestartSource, so losing every byte moved since a daemon restart cannot be detected")
		return
	}
	rig.RestartSource()
	rig.FeedTraffic(Subject, 300, 400)
	afterRestart, err := bound.Traffic.CollectTraffic(ctx)
	if err != nil {
		r.fail("traffic/collects", "CollectTraffic after a source restart failed: %v", err)
		return
	}
	if got := total(afterRestart); got != 700 {
		r.fail("traffic/restart-counts-from-zero", "reported %d bytes after a daemon restart, want 700; a counter that went backwards means the source restarted, and the whole new reading is unbilled traffic", got)
	}
	if got := billedTo(afterRestart, Subject); got != 700 {
		r.fail("traffic/attribution", "%d of 700 post-restart bytes were billed to %q", got, Subject)
	}
}

func billedTo(deltas []core.TrafficDelta, email string) int64 {
	var sum int64
	for _, d := range deltas {
		if d.Email == email {
			sum += d.Up + d.Down
		}
	}
	return sum
}

func total(deltas []core.TrafficDelta) int64 {
	var sum int64
	for _, d := range deltas {
		sum += d.Up + d.Down
	}
	return sum
}
