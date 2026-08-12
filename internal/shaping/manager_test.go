package shaping_test

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/shaping"
	"github.com/Arman2122/p-ui/internal/shaping/shapetest"
)

const (
	device = "pwg7"
	mirror = "pifb7"
)

var (
	alice4 = netip.MustParsePrefix("10.8.0.4/32")
	alice6 = netip.MustParsePrefix("fd00::4/128")
	bob4   = netip.MustParsePrefix("10.8.0.5/32")
)

// The default class, written out rather than derived, so a change to how it is
// built shows up here rather than in a customer's throughput.
const (
	rootIn      = "qdisc+ qdisc pwg7 htb handle 1:0 parent ffff:ffff"
	defaultIn   = "class+ class pwg7 1:ffff rate 125000000000 ceil 125000000000"
	defaultLeaf = "qdisc+ qdisc pwg7 sfq handle none parent 1:ffff"
)

func want(subjects ...shaping.Subject) []shaping.DeviceWant {
	return []shaping.DeviceWant{{Device: device, Subjects: subjects}}
}

func subject(id string, down, up int64, prefixes ...netip.Prefix) shaping.Subject {
	keys := make([]shaping.Key, 0, len(prefixes))
	for _, prefix := range prefixes {
		keys = append(keys, shaping.Key{Prefix: prefix})
	}
	return shaping.Subject{ID: id, Keys: keys, Limits: shaping.Limits{DownBps: down, UpBps: up}}
}

func live(t *testing.T, links ...string) (*shaping.Manager, *shapetest.Kernel) {
	t.Helper()
	kernel := shapetest.New()
	kernel.AddLink(links...)
	return shaping.NewManager(kernel), kernel
}

func converge(t *testing.T, m *shaping.Manager, w []shaping.DeviceWant) {
	t.Helper()
	if err := m.Converge(context.Background(), w); err != nil {
		t.Fatalf("Converge: %v", err)
	}
}

func assertOps(t *testing.T, kernel *shapetest.Kernel, want []string) {
	t.Helper()
	got := kernel.Ops()
	if !slices.Equal(got, want) {
		t.Fatalf("ops\n got %s\nwant %s", strings.Join(got, "\n      "), strings.Join(want, "\n      "))
	}
}

/*
TestDownloadOnlySubjectTouchesNoMirror is the download half of the direction
split, asserted on the exact op log.

An implementation that installs both trees unconditionally passes any "is the
rate right" check and fails here: it would create a mirror device, an ingress
hook and a redirect filter for a user with no upload limit at all.
*/
func TestDownloadOnlySubjectTouchesNoMirror(t *testing.T) {
	m, kernel := live(t, device)
	converge(t, m, want(subject("alice", 10_000_000, 0, alice4)))

	assertOps(t, kernel, []string{
		rootIn, defaultIn, defaultLeaf,
		"class+ class pwg7 1:10 rate 1250000 ceil 1250000",
		"qdisc+ qdisc pwg7 sfq handle none parent 1:10",
		"filter+ filter pwg7 parent 1:0 prio 100 dst_ip 10.8.0.4/32 classid 1:10",
	})
	if devices := kernel.Devices(); slices.Contains(devices, mirror) {
		t.Fatalf("a download-only subject created the upload mirror: %v", devices)
	}
}

// TestUploadOnlySubjectBuildsTheMirrorAndNoDownloadClass is the other half: the
// whole tree moves to the mirror and the device itself carries only the redirect.
func TestUploadOnlySubjectBuildsTheMirrorAndNoDownloadClass(t *testing.T) {
	m, kernel := live(t, device)
	converge(t, m, want(subject("alice", 0, 10_000_000, alice4)))

	assertOps(t, kernel, []string{
		"ifb+ pifb7",
		"qdisc+ qdisc pifb7 htb handle 1:0 parent ffff:ffff",
		"class+ class pifb7 1:ffff rate 125000000000 ceil 125000000000",
		"qdisc+ qdisc pifb7 sfq handle none parent 1:ffff",
		"class+ class pifb7 1:10 rate 1250000 ceil 1250000",
		"qdisc+ qdisc pifb7 sfq handle none parent 1:10",
		"filter+ filter pifb7 parent 1:0 prio 100 src_ip 10.8.0.4/32 classid 1:10",
		"qdisc+ qdisc pwg7 clsact handle ffff:0 parent ffff:fff1",
		"filter+ filter pwg7 parent ffff:fff2 prio 100 src_ip 10.8.0.4/32 redirect pifb7",
	})
}

// TestSymmetricLimitIsTheSameNumberTwice pins that there is no special case: a
// symmetric limit is two independent trees carrying one number, not a flag.
func TestSymmetricLimitIsTheSameNumberTwice(t *testing.T) {
	m, kernel := live(t, device)
	converge(t, m, want(subject("alice", 10_000_000, 10_000_000, alice4)))

	tree := kernel.Tree()
	for _, object := range []string{
		"class pwg7 1:10 rate 1250000 ceil 1250000",
		"class pifb7 1:10 rate 1250000 ceil 1250000",
	} {
		if !slices.Contains(tree, object) {
			t.Fatalf("%q is missing from\n%s", object, strings.Join(tree, "\n"))
		}
	}
}

/*
TestConvergeIsIdempotent is the anti-churn property, and it is deliberately a
property rather than an arithmetic assertion.

It catches every residual quantisation source at once — canonicalisation, netlink
units, kernel rounding — whichever turns out to exist. The rate is 12345678 bit/s
on purpose: HTB stores 1543209 bytes/s and reads back 12345672 bit/s, so a diff in
bits differs forever and issues a ClassChange every single pass.
*/
func TestConvergeIsIdempotent(t *testing.T) {
	cases := []struct {
		name    string
		subject shaping.Subject
	}{
		{"download only", subject("alice", 12_345_678, 0, alice4)},
		{"upload only", subject("alice", 0, 12_345_678, alice4)},
		{"both directions, dual stack", subject("alice", 12_345_678, 7_654_321, alice4, alice6)},
		{"nothing shaped at all", subject("alice", 0, 0, alice4)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, kernel := live(t, device)
			w := want(tc.subject)
			converge(t, m, w)
			before := kernel.Tree()
			kernel.ResetOps()

			converge(t, m, w)
			if writes := kernel.Writes(); writes != 0 {
				t.Fatalf("a second converged pass issued %d writes: %v", writes, kernel.Ops())
			}
			if kernel.Snapshots() == 0 {
				t.Fatal("the second pass read nothing, so it proved nothing")
			}
			if after := kernel.Tree(); !slices.Equal(before, after) {
				t.Fatalf("the tree moved without a write\nbefore %v\nafter  %v", before, after)
			}
		})
	}
}

/*
TestATierCrossingIsOneClassChange is the live-edit requirement, met by the
mechanism rather than by extra machinery.

A delete-and-re-add implementation passes a naive "is the rate right" assertion
and fails here — and in the kernel it would drop the class's byte counters and
shed the in-flight window of the very connection the throttle exists to slow.
*/
func TestATierCrossingIsOneClassChange(t *testing.T) {
	m, kernel := live(t, device)
	converge(t, m, want(subject("alice", 100_000_000, 0, alice4)))
	kernel.ResetOps()

	converge(t, m, want(subject("alice", 10_000_000, 0, alice4)))
	assertOps(t, kernel, []string{"class~ class pwg7 1:10 rate 1250000 ceil 1250000"})
}

/*
TestATrafficResetReleasesOnlyThatClient is the renewal path.

Unlimited is the ABSENCE of a class, not a class at a huge number: the renewed
client falls into the explicit default and stops being classified at all, while
everyone else's tree is untouched. No reset code anywhere is edited to make this
happen — the level-triggered pass simply derives tier 0 again.
*/
func TestATrafficResetReleasesOnlyThatClient(t *testing.T) {
	m, kernel := live(t, device)
	converge(t, m, want(
		subject("alice", 2_000_000, 0, alice4),
		subject("bob", 10_000_000, 0, bob4),
	))
	kernel.ResetOps()

	converge(t, m, want(
		subject("alice", 0, 0, alice4),
		subject("bob", 10_000_000, 0, bob4),
	))
	assertOps(t, kernel, []string{
		"filter- filter pwg7 parent 1:0 prio 100 dst_ip 10.8.0.4/32 classid 1:10",
		"qdisc- qdisc pwg7 sfq handle none parent 1:10",
		"class- class pwg7 1:10 rate 250000 ceil 250000",
	})
	if !slices.Contains(kernel.Tree(), "class pwg7 1:11 rate 1250000 ceil 1250000") {
		t.Fatalf("one client's renewal disturbed another's class: %v", kernel.Tree())
	}
}

/*
TestTeardownRunsFilterThenLeafThenClass pins the order the kernel enforces.

Measured: a class a filter still points at answers EBUSY, so removing the filter
is both what stops classification and what unpins the class — structurally the
same rule as egress's "removing the rule is what stops traffic".
*/
func TestTeardownRunsFilterThenLeafThenClass(t *testing.T) {
	m, kernel := live(t, device)
	converge(t, m, want(
		subject("alice", 10_000_000, 0, alice4),
		subject("bob", 10_000_000, 0, bob4),
	))
	kernel.ResetOps()

	converge(t, m, want(subject("bob", 10_000_000, 0, bob4)))
	assertOps(t, kernel, []string{
		"filter- filter pwg7 parent 1:0 prio 100 dst_ip 10.8.0.4/32 classid 1:10",
		"qdisc- qdisc pwg7 sfq handle none parent 1:10",
		"class- class pwg7 1:10 rate 1250000 ceil 1250000",
	})
	if tree := kernel.Tree(); slices.Contains(tree, "class pwg7 1:10 rate 1250000 ceil 1250000") {
		t.Fatalf("alice's class survived her removal: %v", tree)
	}
}

/*
TestDualStackSharesOneClass is the v6 half of the mechanism, and it is a peer of
v4 rather than a follow-up.

One class and two filters: a dual-stack user's two families share one budget, so
a per-prefix class would sell them twice their contracted rate. The two filters
sit at DIFFERENT priorities because the kernel keys a filter chain on (protocol,
priority) and answers EINVAL to a second family at a priority one already holds.
*/
func TestDualStackSharesOneClass(t *testing.T) {
	m, kernel := live(t, device)
	converge(t, m, want(subject("alice", 10_000_000, 0, alice4, alice6)))

	assertOps(t, kernel, []string{
		rootIn, defaultIn, defaultLeaf,
		"class+ class pwg7 1:10 rate 1250000 ceil 1250000",
		"qdisc+ qdisc pwg7 sfq handle none parent 1:10",
		"filter+ filter pwg7 parent 1:0 prio 100 dst_ip 10.8.0.4/32 classid 1:10",
		"filter+ filter pwg7 parent 1:0 prio 101 dst_ip fd00::4/128 classid 1:10",
	})
}

// TestAKeySetChangeMovesTheSameUser: a client whose v6 address is withdrawn keeps
// one filter and one class, and the class is not rebuilt from scratch.
func TestAKeySetChangeMovesTheSameUser(t *testing.T) {
	m, kernel := live(t, device)
	converge(t, m, want(subject("alice", 10_000_000, 0, alice4, alice6)))
	kernel.ResetOps()

	converge(t, m, want(subject("alice", 10_000_000, 0, alice4)))
	assertOps(t, kernel, []string{
		"filter- filter pwg7 parent 1:0 prio 101 dst_ip fd00::4/128 classid 1:10",
	})
}

/*
TestTwoSubjectsClaimingOnePrefixShapeNeither is the failure a customer cannot
detect for themselves.

Shaping one of them, either one, is a test failure: the victim would be held to
somebody else's rate with nothing on their side to see. Fail-open means both run
unshaped and the error names the prefix.
*/
func TestTwoSubjectsClaimingOnePrefixShapeNeither(t *testing.T) {
	m, kernel := live(t, device)
	err := m.Converge(context.Background(), want(
		subject("alice", 10_000_000, 0, alice4),
		subject("bob", 2_000_000, 0, alice4),
	))
	if !errors.Is(err, shaping.ErrDuplicateKey) {
		t.Fatalf("Converge = %v, want ErrDuplicateKey", err)
	}
	for _, op := range kernel.Ops() {
		if strings.HasPrefix(op, "filter+") {
			t.Fatalf("a contested prefix was still classified: %v", kernel.Ops())
		}
	}
	if tree := kernel.Tree(); slices.ContainsFunc(tree, func(o string) bool {
		return strings.HasPrefix(o, "class pwg7 1:10")
	}) {
		t.Fatalf("a contested prefix got a class: %v", tree)
	}
}

// TestASubjectWithOtherKeysKeepsThem: the duplicate is dropped, not the whole
// subject, so a dual-stack client keeps the family nobody else claims.
func TestASubjectWithOtherKeysKeepsThem(t *testing.T) {
	m, kernel := live(t, device)
	if err := m.Converge(context.Background(), want(
		subject("alice", 10_000_000, 0, alice4, alice6),
		subject("bob", 2_000_000, 0, alice6),
	)); !errors.Is(err, shaping.ErrDuplicateKey) {
		t.Fatalf("Converge = %v, want ErrDuplicateKey", err)
	}
	tree := kernel.Tree()
	if !slices.Contains(tree, "filter pwg7 parent 1:0 prio 100 dst_ip 10.8.0.4/32 classid 1:10") {
		t.Fatalf("alice lost the family nobody contested: %v", tree)
	}
	if slices.ContainsFunc(tree, func(o string) bool { return strings.Contains(o, "fd00::4") }) {
		t.Fatalf("the contested family was still classified: %v", tree)
	}
}

func TestRefusedSelectors(t *testing.T) {
	cases := []struct {
		name    string
		subject shaping.Subject
		want    string
	}{
		{
			name:    "a subnet would shape everyone behind it as one user",
			subject: shaping.Subject{ID: "alice", Keys: []shaping.Key{{Prefix: netip.MustParsePrefix("10.8.0.0/24")}}, Limits: shaping.Limits{DownBps: 1}},
			want:    "not a host prefix",
		},
		{
			name:    "a mark has no mechanism in this version",
			subject: shaping.Subject{ID: "alice", Keys: []shaping.Key{{Mark: 42}}, Limits: shaping.Limits{DownBps: 1}},
			want:    "no mechanism in this version installs",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, kernel := live(t, device)
			err := m.Converge(context.Background(), want(tc.subject))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Converge = %v, want an error naming %q", err, tc.want)
			}
			if writes := kernel.Writes(); writes != 0 {
				t.Fatalf("a refused selector still wrote %d objects: %v", writes, kernel.Ops())
			}
		})
	}
}

// TestAnUnownedDeviceIsRefused: a name is not permission. A tree installed on an
// operator's own interface throttles traffic this panel does not serve.
func TestAnUnownedDeviceIsRefused(t *testing.T) {
	for _, name := range []string{"eth0", "pwgtest", "pifb007", "pwg0"} {
		t.Run(name, func(t *testing.T) {
			m, kernel := live(t, name)
			err := m.Converge(context.Background(), []shaping.DeviceWant{
				{Device: name, Subjects: []shaping.Subject{subject("alice", 10_000_000, 0, alice4)}},
			})
			if !errors.Is(err, shaping.ErrNotOwned) {
				t.Fatalf("Converge(%q) = %v, want ErrNotOwned", name, err)
			}
			if writes := kernel.Writes(); writes != 0 {
				t.Fatalf("a refused device was still written to: %v", kernel.Ops())
			}
		})
	}
}

// TestAnAbsentDeviceIsRetryable: the core owns the device and it is gone between
// a restart and the next reconcile. Its objects died with it, so nothing is stale.
func TestAnAbsentDeviceIsRetryable(t *testing.T) {
	m, kernel := live(t)
	converge(t, m, want(subject("alice", 10_000_000, 0, alice4)))
	if writes := kernel.Writes(); writes != 0 {
		t.Fatalf("an absent device was written to: %v", kernel.Ops())
	}

	kernel.AddLink(device)
	converge(t, m, want(subject("alice", 10_000_000, 0, alice4)))
	if !slices.Contains(kernel.Tree(), "class pwg7 1:10 rate 1250000 ceil 1250000") {
		t.Fatalf("the pass after the device appeared did not converge: %v", kernel.Tree())
	}
}

/*
TestASnapshotFailureChangesNothing is the no-partial-view rule.

Converging from a snapshot that failed is exactly what the no-fingerprint-cache
design exists to prevent: the previously converged state is safe, and a pass that
cannot see the device must leave it alone rather than rebuild it blind.
*/
func TestASnapshotFailureChangesNothing(t *testing.T) {
	m, kernel := live(t, device)
	converge(t, m, want(subject("alice", 10_000_000, 0, alice4)))
	before := kernel.Tree()
	kernel.ResetOps()

	kernel.SnapshotErr = map[string]error{device: errors.New("netlink is unhappy")}
	if err := m.Converge(context.Background(), want()); err == nil {
		t.Fatal("a failed snapshot converged silently")
	}
	if writes := kernel.Writes(); writes != 0 {
		t.Fatalf("a failed snapshot still wrote %d objects: %v", writes, kernel.Ops())
	}
	if after := kernel.Tree(); !slices.Equal(before, after) {
		t.Fatalf("a failed snapshot changed the tree\nbefore %v\nafter  %v", before, after)
	}
}

// TestOneDeviceFailingDoesNotStopAnother: every device converges on its own, so a
// single unreadable interface cannot leave the rest of the host unshaped.
func TestOneDeviceFailingDoesNotStopAnother(t *testing.T) {
	kernel := shapetest.New()
	kernel.AddLink("pwg7", "pwg8")
	kernel.SnapshotErr = map[string]error{"pwg7": errors.New("netlink is unhappy")}
	m := shaping.NewManager(kernel)

	err := m.Converge(context.Background(), []shaping.DeviceWant{
		{Device: "pwg7", Subjects: []shaping.Subject{subject("alice", 10_000_000, 0, alice4)}},
		{Device: "pwg8", Subjects: []shaping.Subject{subject("bob", 10_000_000, 0, bob4)}},
	})
	if err == nil || !strings.Contains(err.Error(), "pwg7") {
		t.Fatalf("Converge = %v, want an error naming pwg7", err)
	}
	if !slices.Contains(kernel.Tree(), "class pwg8 1:10 rate 1250000 ceil 1250000") {
		t.Fatalf("pwg7's failure suppressed pwg8: %v", kernel.Tree())
	}
}

/*
TestAForeignRootQdiscIsReportedAndNotDeleted.

The plane has no ChangeQdisc, so "repairing" a root somebody else wrote means
deleting their whole tree. The panel adopts a correct root it did not create and
never touches one it cannot recognise.
*/
func TestAForeignRootQdiscIsReportedAndNotDeleted(t *testing.T) {
	m, kernel := live(t, device)
	foreign := shaping.QdiscSpec{Device: device, Type: "fq_codel", Handle: 0x80010000, Parent: 0xffffffff}
	kernel.Seed([]shaping.QdiscSpec{foreign}, nil, nil)

	err := m.Converge(context.Background(), want(subject("alice", 10_000_000, 0, alice4)))
	if !errors.Is(err, shaping.ErrForeignObject) {
		t.Fatalf("Converge = %v, want ErrForeignObject", err)
	}
	if !slices.Contains(kernel.Tree(), foreign.String()) {
		t.Fatalf("a foreign root qdisc was deleted: %v", kernel.Tree())
	}
	if writes := kernel.Writes(); writes != 0 {
		t.Fatalf("a device with a foreign root was still written to: %v", kernel.Ops())
	}
}

/*
TestAForeignFilterAheadOfOursIsAnError.

It is an error rather than a log line because it silently eats the packets ours
was installed to classify: a shaper that does nothing while reporting success is
the worst outcome available.
*/
func TestAForeignFilterAheadOfOursIsAnError(t *testing.T) {
	m, kernel := live(t, device)
	converge(t, m, want(subject("alice", 10_000_000, 0, alice4)))
	kernel.Seed(nil, nil, []shaping.FilterSpec{{
		Device: device, Parent: 0x00010000, Priority: 50,
		Match: shaping.MatchDst, Prefix: alice4, ClassID: 0x0001ffff,
	}})
	kernel.ResetOps()

	err := m.Converge(context.Background(), want(subject("alice", 10_000_000, 0, alice4)))
	if !errors.Is(err, shaping.ErrForeignObject) {
		t.Fatalf("Converge = %v, want ErrForeignObject", err)
	}
	if writes := kernel.Writes(); writes != 0 {
		t.Fatalf("a shadowed device was still written to: %v", kernel.Ops())
	}
}

// TestAFilterBehindOursIsLeftAlone: only a filter AHEAD of ours can steal a
// packet, so one behind is an operator's business and never an error.
func TestAFilterBehindOursIsLeftAlone(t *testing.T) {
	m, kernel := live(t, device)
	behind := shaping.FilterSpec{
		Device: device, Parent: 0x00010000, Priority: 900,
		Match: shaping.MatchDst, Prefix: bob4, ClassID: 0x0001ffff, Handle: 9,
	}
	kernel.Seed(nil, nil, []shaping.FilterSpec{behind})

	converge(t, m, want(subject("alice", 10_000_000, 0, alice4)))
	if !slices.Contains(kernel.Tree(), behind.String()) {
		t.Fatalf("a filter behind ours was deleted: %v", kernel.Tree())
	}
}

/*
TestAnExistingTreeIsAdoptedAfterARestart.

The panel persists no map of class ids: the kernel's own filter dump answers
selector -> class -> rate, so a correct tree an earlier process built is adopted
rather than rebuilt. A persisted map would be a second source of truth that rots.
*/
func TestAnExistingTreeIsAdoptedAfterARestart(t *testing.T) {
	m, kernel := live(t, device)
	converge(t, m, want(subject("alice", 10_000_000, 0, alice4)))

	// A brand new manager is exactly what a panel restart produces: no memory of
	// which minor anybody had, and the same kernel underneath.
	fresh := shaping.NewManager(kernel)
	kernel.ResetOps()
	converge(t, fresh, want(subject("alice", 10_000_000, 0, alice4)))
	if writes := kernel.Writes(); writes != 0 {
		t.Fatalf("a restarted manager rebuilt an adoptable tree: %v", kernel.Ops())
	}
}

/*
TestAFilterPointingAtTheWrongClassIsRepaired.

Out-of-band damage is corrected on the next pass because the diff reads the
kernel and not its own memory. The assertion is on the READBACK rather than on a
class id: a contested minor is adopted by nobody, so which minor each user lands
on is deliberately not part of the contract — that they end up on different
classes at their own contracted rates is.
*/
func TestAFilterPointingAtTheWrongClassIsRepaired(t *testing.T) {
	m, kernel := live(t, device)
	w := want(subject("alice", 10_000_000, 0, alice4), subject("bob", 2_000_000, 0, bob4))
	converge(t, m, w)

	// Alice's filter is re-pointed at Bob's class: the exact shape of the bug that
	// shapes one user as another, and the one a customer cannot see for themselves.
	kernel.Retarget(alice4, 0x00010011)
	converge(t, m, w)

	got, err := m.Enforced(context.Background(), w[0])
	if err != nil {
		t.Fatalf("Enforced: %v", err)
	}
	if got["alice"].DownBps != 10_000_000 || got["bob"].DownBps != 2_000_000 {
		t.Fatalf("after the repair the kernel holds %+v, want alice 10000000 and bob 2000000", got)
	}
	kernel.ResetOps()
	converge(t, m, w)
	if writes := kernel.Writes(); writes != 0 {
		t.Fatalf("the repaired tree did not settle: %v", kernel.Ops())
	}
}

// TestAStrandedClassIsDeleted: a class no selector points at holds a minor and
// shapes nothing, and only the kernel can say it is there.
func TestAStrandedClassIsDeleted(t *testing.T) {
	m, kernel := live(t, device)
	converge(t, m, want(subject("alice", 10_000_000, 0, alice4)))
	kernel.Seed(nil, []shaping.ClassSpec{{
		Device: device, Handle: 0x00010042, Parent: 0x00010000,
		RateBytesPerSec: 1, CeilBytesPerSec: 1,
	}}, nil)
	kernel.ResetOps()

	converge(t, m, want(subject("alice", 10_000_000, 0, alice4)))
	assertOps(t, kernel, []string{"class- class pwg7 1:42 rate 1 ceil 1"})
}

/*
TestTheMirrorSurvivesItsParentAndIsThenCollected.

Measured: deleting the core's device takes its clsact qdisc and its mirred
filters with it while the ifb device SURVIVES. Without the link enumeration this
GC walks, a panel leaks one interface per inbound per lifetime.
*/
func TestTheMirrorSurvivesItsParentAndIsThenCollected(t *testing.T) {
	m, kernel := live(t, device)
	converge(t, m, want(subject("alice", 0, 10_000_000, alice4)))
	if !slices.Contains(kernel.Devices(), mirror) {
		t.Fatalf("the mirror was never created: %v", kernel.Devices())
	}

	kernel.DelLink(device)
	if !slices.Contains(kernel.Devices(), mirror) {
		t.Fatalf("the fake let the mirror die with its parent; the kernel does not: %v", kernel.Devices())
	}
	converge(t, m, nil)
	if slices.Contains(kernel.Devices(), mirror) {
		t.Fatalf("a stranded mirror was not collected: %v", kernel.Devices())
	}
}

// TestAMirrorStaysWhileItsUploadSubjectDoes: the GC keys on the want and not on
// whether the parent device happens to be up, so a core restart cannot churn it.
func TestAMirrorStaysWhileItsUploadSubjectDoes(t *testing.T) {
	m, kernel := live(t, device)
	converge(t, m, want(subject("alice", 0, 10_000_000, alice4)))
	kernel.DelLink(device)
	kernel.ResetOps()

	converge(t, m, want(subject("alice", 0, 10_000_000, alice4)))
	for _, op := range kernel.Ops() {
		if strings.HasPrefix(op, "ifb-") {
			t.Fatalf("the mirror was collected while its subject still wanted it: %v", kernel.Ops())
		}
	}
}

// TestDroppingTheUploadLimitRemovesTheHookBeforeTheMirror: a mirred filter naming
// a device that is going away must go first, or it is left pointing at nothing.
func TestDroppingTheUploadLimitRemovesTheHookBeforeTheMirror(t *testing.T) {
	m, kernel := live(t, device)
	converge(t, m, want(subject("alice", 0, 10_000_000, alice4)))
	kernel.ResetOps()

	converge(t, m, want(subject("alice", 0, 0, alice4)))
	assertOps(t, kernel, []string{
		"filter- filter pwg7 parent ffff:fff2 prio 100 src_ip 10.8.0.4/32 redirect pifb7",
		"qdisc- qdisc pwg7 clsact handle ffff:0 parent ffff:fff1",
		"ifb- pifb7",
	})
}

// TestZeroSubjectsTearsTheDeviceDown is the contract that lets the caller send
// every shapeable device: an empty want means no owned objects, not "skip me".
func TestZeroSubjectsTearsTheDeviceDown(t *testing.T) {
	m, kernel := live(t, device)
	converge(t, m, want(subject("alice", 10_000_000, 0, alice4)))
	kernel.ResetOps()

	converge(t, m, want())
	assertOps(t, kernel, []string{
		"filter- filter pwg7 parent 1:0 prio 100 dst_ip 10.8.0.4/32 classid 1:10",
		"qdisc- qdisc pwg7 sfq handle none parent 1:10",
		"class- class pwg7 1:10 rate 1250000 ceil 1250000",
		"qdisc- qdisc pwg7 sfq handle none parent 1:ffff",
		"class- class pwg7 1:ffff rate 125000000000 ceil 125000000000",
		"qdisc- qdisc pwg7 htb handle 1:0 parent ffff:ffff",
	})
	for _, object := range kernel.Tree() {
		if strings.Contains(object, "1:") && !strings.Contains(object, "noqueue") {
			t.Fatalf("an owned object survived the teardown: %v", kernel.Tree())
		}
	}
}

/*
TestEnforcedReadsTheKernelAndNotThePush.

The readback is what the UI shows and what a conformance check compares against.
Reporting the value the panel pushed would certify a shaper that never installed
anything, which is the exact failure the no-cache rule exists to make impossible.
*/
func TestEnforcedReadsTheKernelAndNotThePush(t *testing.T) {
	m, kernel := live(t, device)
	w := want(subject("alice", 10_000_000, 4_000_000, alice4), subject("bob", 2_000_000, 0, bob4))
	converge(t, m, w)

	got, err := m.Enforced(context.Background(), w[0])
	if err != nil {
		t.Fatalf("Enforced: %v", err)
	}
	// Truncated to the kernel's own unit and back: 10e6 bit/s is 1250000 B/s.
	if want := (shaping.Limits{DownBps: 10_000_000, UpBps: 4_000_000}); got["alice"] != want {
		t.Fatalf("Enforced[alice] = %+v, want %+v", got["alice"], want)
	}
	if want := (shaping.Limits{DownBps: 2_000_000}); got["bob"] != want {
		t.Fatalf("Enforced[bob] = %+v, want %+v", got["bob"], want)
	}

	// The kernel is the authority: a class retuned behind the panel's back must
	// be reported at what it now holds, not at what was pushed.
	kernel.Retune(device, 0x00010010, 125_000)
	got, err = m.Enforced(context.Background(), w[0])
	if err != nil {
		t.Fatalf("Enforced: %v", err)
	}
	if got["alice"].DownBps != 1_000_000 {
		t.Fatalf("Enforced[alice].DownBps = %d, want the kernel's own 1000000", got["alice"].DownBps)
	}
}

// TestEnforcedReportsNothingForAnUnshapedSubject: absence is the honest answer,
// not a zero that reads like a limit of zero.
func TestEnforcedReportsNothingForAnUnshapedSubject(t *testing.T) {
	m, _ := live(t, device)
	w := want(subject("alice", 0, 0, alice4))
	converge(t, m, w)

	got, err := m.Enforced(context.Background(), w[0])
	if err != nil {
		t.Fatalf("Enforced: %v", err)
	}
	if _, reported := got["alice"]; reported {
		t.Fatalf("Enforced reported %+v for a subject with no limit at all", got)
	}
}

// TestTwoDevicesDerivingOneMirrorIsRefused: pwg7 and peg7 map to the same mirror,
// so shaping both would put one inbound's upload budget on the other's traffic.
func TestTwoDevicesDerivingOneMirrorIsRefused(t *testing.T) {
	kernel := shapetest.New()
	kernel.AddLink("pwg7", "peg7")
	m := shaping.NewManager(kernel)

	err := m.Converge(context.Background(), []shaping.DeviceWant{
		{Device: "pwg7", Subjects: []shaping.Subject{subject("alice", 0, 10_000_000, alice4)}},
		{Device: "peg7", Subjects: []shaping.Subject{subject("bob", 0, 10_000_000, bob4)}},
	})
	if err == nil || !strings.Contains(err.Error(), "both derive mirror pifb7") {
		t.Fatalf("Converge = %v, want a refusal naming the shared mirror", err)
	}
}

/*
TestPreflightNamesTheMissingModule.

A failure disables shaping and never stops the panel, per Core.Preflight's own
rule, so the report has to say WHICH module an operator would have to load.
*/
func TestPreflightNamesTheMissingModule(t *testing.T) {
	cases := []struct {
		name   string
		op     string
		module string
	}{
		{"no htb", "qdisc+ qdisc pui-shape0 htb handle 1:0 parent ffff:ffff", "sch_htb"},
		{"no sfq", "qdisc+ qdisc pui-shape0 sfq handle none parent 1:10", "sch_sfq"},
		{"no flower", "filter+ filter pui-shape0 parent 1:0 prio 100 dst_ip 192.0.2.1/32 classid 1:10", "cls_flower"},
		{"no clsact", "qdisc+ qdisc pui-shape0 clsact handle ffff:0 parent ffff:fff1", "clsact"},
		{"no mirred", "filter+ filter pui-shape0 parent ffff:fff2 prio 100 src_ip 192.0.2.1/32 redirect pui-shape0", "act_mirred"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kernel := shapetest.New()
			kernel.Fail = map[string]error{tc.op: errors.New("this kernel says no")}
			report := shaping.NewManager(kernel).Preflight(context.Background())
			if report.OK() {
				t.Fatalf("preflight passed a host that cannot %s", tc.op)
			}
			if !errors.Is(report.Err(), shaping.ErrModuleMissing) || !strings.Contains(report.Err().Error(), tc.module) {
				t.Fatalf("Preflight = %v, want an ErrModuleMissing naming %s", report.Err(), tc.module)
			}
		})
	}
}

// TestPreflightLeavesTheHostAsItFoundIt: the probe is a reversible write, and a
// probe device left behind would be an interface leak on every panel start.
func TestPreflightLeavesTheHostAsItFoundIt(t *testing.T) {
	kernel := shapetest.New()
	kernel.AddLink(device)
	before := kernel.Devices()

	if report := shaping.NewManager(kernel).Preflight(context.Background()); !report.OK() {
		t.Fatalf("Preflight on a capable host: %v", report.Err())
	}
	if after := kernel.Devices(); !slices.Equal(before, after) {
		t.Fatalf("preflight changed the host's devices\nbefore %v\nafter  %v", before, after)
	}
	if tree := kernel.Tree(); len(tree) != 1 || !strings.Contains(tree[0], "noqueue") {
		t.Fatalf("preflight left objects behind: %v", tree)
	}
}

// clsactOn is the shared hook as the kernel reports it, so a test can remove it
// out of band exactly as a device recreate does.
func clsactOn(dev string) shaping.QdiscSpec {
	return shaping.QdiscSpec{Device: dev, Type: "clsact", Handle: 0xffff0000, Parent: 0xfffffff1}
}

/*
TestAForeignIngressFilterAheadOfOursIsAnError.

The download half has always treated this as fatal; the ingress half counted it
and carried on. tcf_classify returns on the FIRST filter that yields a verdict,
so a foreign rule ahead of prio 100 swallows the packet and the panel's mirred
redirect never runs — upload is then permanently unshaped while Converge says nil.
*/
func TestAForeignIngressFilterAheadOfOursIsAnError(t *testing.T) {
	m, kernel := live(t, device)
	w := want(subject("alice", 0, 10_000_000, alice4))
	converge(t, m, w)

	kernel.Seed(nil, nil, []shaping.FilterSpec{{Device: device, Parent: 0xfffffff2, Priority: 1}})
	kernel.ResetOps()

	err := m.Converge(context.Background(), w)
	if !errors.Is(err, shaping.ErrForeignObject) {
		t.Fatalf("Converge = %v, want ErrForeignObject", err)
	}
	if writes := kernel.Writes(); writes != 0 {
		t.Fatalf("a shadowed ingress hook was still written to: %v", kernel.Ops())
	}
}

// TestAnIngressFilterBehindOursIsLeftAlone: the same split the download tree
// makes. Only a filter ahead of ours can steal a packet.
func TestAnIngressFilterBehindOursIsLeftAlone(t *testing.T) {
	m, kernel := live(t, device)
	behind := shaping.FilterSpec{Device: device, Parent: 0xfffffff2, Priority: 900, Handle: 7}
	kernel.Seed(nil, nil, []shaping.FilterSpec{behind})

	converge(t, m, want(subject("alice", 0, 10_000_000, alice4)))
	if !slices.Contains(kernel.Tree(), behind.String()) {
		t.Fatalf("an ingress filter behind ours was deleted: %v", kernel.Tree())
	}
}

/*
TestAnOperatorsEgressFilterKeepsTheSharedHook.

One clsact carries both blocks, so deleting it takes an administrator's tc-egress
filters and BPF programs with it. The retention check has to see the half this
panel never writes, which is why the snapshot reads it.
*/
func TestAnOperatorsEgressFilterKeepsTheSharedHook(t *testing.T) {
	m, kernel := live(t, device)
	operator := shaping.FilterSpec{Device: device, Parent: shapetest.EgressBlock, Priority: 1, Handle: 3}
	kernel.Seed([]shaping.QdiscSpec{clsactOn(device)}, nil, []shaping.FilterSpec{operator})

	converge(t, m, want(subject("alice", 0, 10_000_000, alice4)))
	kernel.ResetOps()

	// The routine edit: the client moves to a download-only tier, so this panel
	// wants nothing on the hook any more.
	converge(t, m, want(subject("alice", 10_000_000, 0, alice4)))
	for _, op := range kernel.Ops() {
		if strings.Contains(op, "clsact") {
			t.Fatalf("the shared hook was removed under an operator's own filter: %v", kernel.Ops())
		}
	}
	if !slices.Contains(kernel.Tree(), operator.String()) {
		t.Fatalf("an operator's egress filter was deleted with the hook: %v", kernel.Tree())
	}
}

/*
TestAMirrorIsNotCollectedWhileALiveFilterRedirectsIntoIt.

Reachable today by disabling an inbound: the device leaves DesiredInstances at
once and its teardown is a different job on its own schedule. The kernel answers
TC_ACT_SHOT to a redirect at a departed device, so reaping the mirror early does
not leave the client unshaped — it disconnects them.
*/
func TestAMirrorIsNotCollectedWhileALiveFilterRedirectsIntoIt(t *testing.T) {
	m, kernel := live(t, device)
	converge(t, m, want(subject("alice", 0, 10_000_000, alice4)))
	kernel.ResetOps()

	converge(t, m, nil)
	if !slices.Contains(kernel.Devices(), mirror) {
		t.Fatalf("the mirror was reaped while %s still redirected into it: %v", device, kernel.Ops())
	}
	if writes := kernel.Writes(); writes != 0 {
		t.Fatalf("a device left out of the want was still changed: %v", kernel.Ops())
	}

	// The parent's teardown is what makes the mirror collectable, and the very
	// next pass takes it: keeping it is a delay, never a leak.
	kernel.DelLink(device)
	converge(t, m, nil)
	if slices.Contains(kernel.Devices(), mirror) {
		t.Fatalf("a mirror nothing points at survived: %v", kernel.Devices())
	}
}

/*
TestEnforcedReportsNoUploadWithoutTheRedirect.

An intact mirror tree that nothing feeds is indistinguishable from an enforcing
one unless the readback follows the whole chain. This is the state a wgkernel
device recreate leaves: the clsact and every mirred filter die with the device
while the ifb mirror survives.
*/
func TestEnforcedReportsNoUploadWithoutTheRedirect(t *testing.T) {
	m, kernel := live(t, device)
	w := want(subject("alice", 10_000_000, 10_000_000, alice4))
	converge(t, m, w)

	if err := kernel.DelQdisc(context.Background(), clsactOn(device)); err != nil {
		t.Fatalf("remove the hook out of band: %v", err)
	}
	got, err := m.Enforced(context.Background(), w[0])
	if err != nil {
		t.Fatalf("Enforced: %v", err)
	}
	if want := (shaping.Limits{DownBps: 10_000_000}); got["alice"] != want {
		t.Fatalf("Enforced[alice] = %+v, want %+v: the mirror tree is intact but nothing feeds it", got["alice"], want)
	}
}

// crowd is one device carrying n shaped users, each on its own host prefix.
func crowd(n int) []shaping.DeviceWant {
	subjects := make([]shaping.Subject, 0, n)
	for i := range n {
		addr := netip.AddrFrom4([4]byte{10, byte(i >> 16), byte(i >> 8), byte(i)})
		subjects = append(subjects, subject(fmt.Sprintf("user%d", i), 10_000_000, 10_000_000,
			netip.PrefixFrom(addr, 32)))
	}
	return want(subjects...)
}

// BenchmarkSteadyStatePass times the pass that issues no write at all — the one
// the cron runs every 10s and the only one whose cost is paid continuously.
func BenchmarkSteadyStatePass(b *testing.B) {
	for _, n := range []int{1000, 2000, 4000, 8000} {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			kernel := shapetest.New()
			kernel.AddLink(device)
			m := shaping.NewManager(kernel)
			w := crowd(n)
			if err := m.Converge(context.Background(), w); err != nil {
				b.Fatalf("build: %v", err)
			}
			b.ResetTimer()
			for b.Loop() {
				if err := m.Converge(context.Background(), w); err != nil {
					b.Fatalf("Converge: %v", err)
				}
			}
		})
	}
}

/*
TestTheLeafIsNotCodel pins the leaf qdisc, because the one that reads like the
obvious choice is the one that silently breaks the product.

MEASURED, HTTP through a real WireGuard tunnel on 7.0.0, contracted rate against
delivered throughput, with an fq_codel leaf on the kernel's own 5ms target:

	100 Mbit 96%   50 Mbit 96%   20 Mbit 96%
	10 Mbit  1.9%  5 Mbit  1.1%  2 Mbit 1.1%   1 Mbit 1.3%

Almost nothing is dropped — CoDel simply signals congestion continuously once one
queue of packets outlasts its target, and TCP settles at about one packet in
flight. It reproduces with a hand-written tc tree and no panel involved, and UDP
at the same rate is unaffected, so it is the AQM's parameter and not this panel,
the tunnel, or HTB. Raising the target to 48ms at 5 Mbit restores 96% — but
vishvananda/netlink v1.3.1 never encodes TCA_FQ_CODEL_TARGET, so from here the
parameter cannot be set at all.

sfq delivers 96% at every rate from 1 Mbit to 100 Mbit, and holds a saturating
class to 3.7ms of added delay at 5 Mbit where fq bloats to 54ms and pfifo to 12ms.
Do not swap this back without re-running that measurement.
*/
func TestTheLeafIsNotCodel(t *testing.T) {
	m, kernel := live(t, device)
	converge(t, m, want(subject("alice", 2_000_000, 2_000_000, alice4)))

	for _, object := range kernel.Tree() {
		if strings.Contains(object, "fq_codel") {
			t.Fatalf("a codel leaf is installed, which delivers ~1%% of a 2 Mbit contract: %q", object)
		}
	}
	var leaves int
	for _, object := range kernel.Tree() {
		if strings.Contains(object, " sfq ") {
			leaves++
		}
	}
	// Both trees, and the default class of each: four in total.
	if leaves != 4 {
		t.Fatalf("sfq leaves = %d, want 4 (a shaped class and a default class on the device and on the mirror):\n%s",
			leaves, strings.Join(kernel.Tree(), "\n"))
	}
}
