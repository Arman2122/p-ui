package egress_test

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/egress"
	"github.com/Arman2122/p-ui/internal/egress/drivers/xraytun"
	"github.com/Arman2122/p-ui/internal/egress/egtest"
)

const rpFilterKey = "net.ipv4.conf.peg1.rp_filter"

// The object set one enabled xray-tun egress with one attached inbound owns,
// written out rather than derived, so a change to the derivation shows up here.
var (
	steadyOps = []string{
		"route+ v4 table 30001 blackhole default metric 4096",
		"route+ v6 table 30001 blackhole default metric 4096",
		"rule+ v4 prio 31001 iif pwg3 lookup 30001",
		"rule+ v6 prio 31001 iif pwg3 lookup 30001",
		"route+ v4 table 30001 default dev peg1 metric 100",
		"route+ v6 table 30001 default dev peg1 metric 100",
		"sysctl net.ipv4.conf.peg1.rp_filter=0",
	}
	steadyRules = []string{
		"v4 prio 31001 iif pwg3 lookup 30001",
		"v6 prio 31001 iif pwg3 lookup 30001",
	}
	steadyRoutes = []string{
		"v4 table 30001 blackhole default metric 4096",
		"v4 table 30001 default dev peg1 metric 100",
		"v6 table 30001 blackhole default metric 4096",
		"v6 table 30001 default dev peg1 metric 100",
	}
)

func row() egress.Egress {
	return egress.Egress{ID: 1, Type: xraytun.Type, Enable: true, Target: "proxy", Ingress: []string{"pwg3"}}
}

// newManager wires the stand-in kernel to the real xray-tun driver: the driver
// decides the device name and the knob, so faking it would fake the thing tested.
func newManager(t *testing.T, k *egtest.Kernel, extra ...egress.Driver) *egress.Manager {
	t.Helper()
	registry := egress.NewRegistry()
	if err := registry.Register(xraytun.New()); err != nil {
		t.Fatalf("register xray-tun: %v", err)
	}
	for _, driver := range extra {
		if err := registry.Register(driver); err != nil {
			t.Fatalf("register %s: %v", driver.Type(), err)
		}
	}
	return egress.New(k, registry)
}

// upHost is a host with the front already created by the core process, address
// and all: a front without its gateway /32 is a device the kernel never produces.
func upHost(t *testing.T) *egtest.Kernel {
	t.Helper()
	k := egtest.New()
	k.AddLink("peg1", rpFilterKey)
	k.AddAddr("peg1", mustGateway(t, 1))
	return k
}

// mustGateway is the address Xray puts on peg<id>, derived rather than written
// out so a change to the derivation reaches the fake too.
func mustGateway(t *testing.T, id int) netip.Prefix {
	t.Helper()
	gateway, err := egress.Gateway(egress.DefaultGatewayBase, id)
	if err != nil {
		t.Fatalf("gateway for egress %d: %v", id, err)
	}
	return gateway
}

func mustEnsure(t *testing.T, m *egress.Manager, e egress.Egress) {
	t.Helper()
	if err := m.Ensure(context.Background(), e); err != nil {
		t.Fatalf("Ensure(%d): %v", e.ID, err)
	}
}

func assertList(t *testing.T, what string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s =\n  %s\nwant\n  %s", what, strings.Join(got, "\n  "), strings.Join(want, "\n  "))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("%s[%d] = %q, want %q\nfull:\n  %s", what, i, got[i], want[i], strings.Join(got, "\n  "))
		}
	}
}

// TestEnsureInstallsContainmentBeforeSelection is the ordering invariant: a rule
// whose table has no match falls through to main and out with the server's address.
func TestEnsureInstallsContainmentBeforeSelection(t *testing.T) {
	k := upHost(t)
	m := newManager(t, k)
	mustEnsure(t, m, row())

	assertList(t, "ops", k.Ops(), steadyOps)
	assertList(t, "rules", k.Rules(), steadyRules)
	assertList(t, "routes", k.Routes(), steadyRoutes)

	for _, family := range []string{"v4", "v6"} {
		blackhole := indexOf(k.Ops(), "route+ "+family+" table 30001 blackhole default metric 4096")
		rule := indexOf(k.Ops(), "rule+ "+family+" prio 31001 iif pwg3 lookup 30001")
		if blackhole < 0 || rule < 0 || blackhole > rule {
			t.Fatalf("%s blackhole is at op %d and its rule at %d; the table must never be selectable before it is contained", family, blackhole, rule)
		}
	}
}

func TestEnsureIsIdempotent(t *testing.T) {
	k := upHost(t)
	m := newManager(t, k)
	mustEnsure(t, m, row())
	k.ResetOps()

	mustEnsure(t, m, row())
	if writes := k.Writes(); writes != 0 {
		t.Fatalf("a converged pass issued %d writes: %v", writes, k.Ops())
	}
}

// TestAdoptsStateFromAnEarlierProcess is the restart case. A manager that remembered
// its own writes instead of reading the host would rebuild all of it.
func TestAdoptsStateFromAnEarlierProcess(t *testing.T) {
	k := upHost(t)
	seedSteadyState(t, k)
	k.SetSysctlValue(rpFilterKey, "0")

	m := newManager(t, k)
	mustEnsure(t, m, row())
	if writes := k.Writes(); writes != 0 {
		t.Fatalf("adopting correct state issued %d writes: %v", writes, k.Ops())
	}
	assertList(t, "rules", k.Rules(), steadyRules)
	assertList(t, "routes", k.Routes(), steadyRoutes)
}

func TestAttachAndDetachAreOneRuleEach(t *testing.T) {
	k := upHost(t)
	m := newManager(t, k)
	mustEnsure(t, m, row())
	k.ResetOps()

	if err := m.Attach(context.Background(), row(), "pwg7"); err != nil {
		t.Fatalf("Attach: %v", err)
	}
	assertList(t, "attach ops", k.Ops(), []string{
		"rule+ v4 prio 31001 iif pwg7 lookup 30001",
		"rule+ v6 prio 31001 iif pwg7 lookup 30001",
	})
	assertList(t, "rules", k.Rules(), []string{
		"v4 prio 31001 iif pwg3 lookup 30001",
		"v4 prio 31001 iif pwg7 lookup 30001",
		"v6 prio 31001 iif pwg3 lookup 30001",
		"v6 prio 31001 iif pwg7 lookup 30001",
	})

	k.ResetOps()
	both := row()
	both.Ingress = []string{"pwg3", "pwg7"}
	if err := m.Detach(context.Background(), both, "pwg3"); err != nil {
		t.Fatalf("Detach: %v", err)
	}
	assertList(t, "detach ops", k.Ops(), []string{
		"rule- v4 prio 31001 iif pwg3 lookup 30001",
		"rule- v6 prio 31001 iif pwg3 lookup 30001",
	})
	assertList(t, "routes", k.Routes(), steadyRoutes)
}

// TestRepairsOutOfBandDamage drives the reconciler's real job: somebody else
// changed the host, and the next pass has to notice from the host itself.
func TestRepairsOutOfBandDamage(t *testing.T) {
	cases := []struct {
		name   string
		damage func(t *testing.T, k *egtest.Kernel)
		ops    []string
		rules  []string
		routes []string
	}{
		{
			name: "a rule was deleted",
			damage: func(t *testing.T, k *egtest.Kernel) {
				t.Helper()
				drop(t, k, egress.RuleSpec{Family: egress.FamilyV4, Priority: 31001, Iif: "pwg3", Table: 30001})
			},
			ops:    []string{"rule+ v4 prio 31001 iif pwg3 lookup 30001"},
			rules:  steadyRules,
			routes: steadyRoutes,
		},
		{
			name: "the blackhole was deleted",
			damage: func(t *testing.T, k *egtest.Kernel) {
				t.Helper()
				dropRoute(t, k, blackhole(egress.FamilyV4))
			},
			ops:    []string{"route+ v4 table 30001 blackhole default metric 4096"},
			rules:  steadyRules,
			routes: steadyRoutes,
		},
		{
			name: "the whole table was flushed",
			damage: func(t *testing.T, k *egtest.Kernel) {
				t.Helper()
				dropRoute(t, k, blackhole(egress.FamilyV4), blackhole(egress.FamilyV6),
					front(egress.FamilyV4, "peg1"), front(egress.FamilyV6, "peg1"))
			},
			ops: []string{
				"route+ v4 table 30001 blackhole default metric 4096",
				"route+ v6 table 30001 blackhole default metric 4096",
				"route+ v4 table 30001 default dev peg1 metric 100",
				"route+ v6 table 30001 default dev peg1 metric 100",
			},
			rules:  steadyRules,
			routes: steadyRoutes,
		},
		{
			name: "the front was repointed at another device",
			damage: func(t *testing.T, k *egtest.Kernel) {
				t.Helper()
				dropRoute(t, k, front(egress.FamilyV4, "peg1"))
				k.AddLink("peg2")
				k.SeedRoute(front(egress.FamilyV4, "peg2"))
			},
			ops: []string{
				"route- v4 table 30001 default dev peg2 metric 100",
				"route+ v4 table 30001 default dev peg1 metric 100",
			},
			rules:  steadyRules,
			routes: steadyRoutes,
		},
		{
			name: "an attachment nobody asked for was left behind",
			damage: func(t *testing.T, k *egtest.Kernel) {
				t.Helper()
				k.SeedRule(egress.RuleSpec{Family: egress.FamilyV4, Priority: 31001, Iif: "pwg99", Table: 30001})
			},
			ops:    []string{"rule- v4 prio 31001 iif pwg99 lookup 30001"},
			rules:  steadyRules,
			routes: steadyRoutes,
		},
		{
			name: "the knob was reset to the host default",
			damage: func(t *testing.T, k *egtest.Kernel) {
				t.Helper()
				k.SetSysctlValue(rpFilterKey, "2")
			},
			ops:    []string{"sysctl net.ipv4.conf.peg1.rp_filter=0"},
			rules:  steadyRules,
			routes: steadyRoutes,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k := upHost(t)
			m := newManager(t, k)
			mustEnsure(t, m, row())
			tc.damage(t, k)
			k.ResetOps()

			mustEnsure(t, m, row())
			assertList(t, "repair ops", k.Ops(), tc.ops)
			assertList(t, "rules", k.Rules(), tc.rules)
			assertList(t, "routes", k.Routes(), tc.routes)
		})
	}
}

// TestFrontIsRetryableWhileItsDeviceIsAbsent is the normal state while the core
// restarts: containment holds, and only the route needing the device waits.
func TestFrontIsRetryableWhileItsDeviceIsAbsent(t *testing.T) {
	k := egtest.New()
	m := newManager(t, k)
	mustEnsure(t, m, row())

	assertList(t, "ops", k.Ops(), []string{
		"route+ v4 table 30001 blackhole default metric 4096",
		"route+ v6 table 30001 blackhole default metric 4096",
		"rule+ v4 prio 31001 iif pwg3 lookup 30001",
		"rule+ v6 prio 31001 iif pwg3 lookup 30001",
	})
	assertList(t, "routes", k.Routes(), []string{
		"v4 table 30001 blackhole default metric 4096",
		"v6 table 30001 blackhole default metric 4096",
	})

	k.AddLink("peg1", rpFilterKey)
	k.ResetOps()
	mustEnsure(t, m, row())
	assertList(t, "ops once the front is up", k.Ops(), []string{
		"route+ v4 table 30001 default dev peg1 metric 100",
		"route+ v6 table 30001 default dev peg1 metric 100",
		"sysctl net.ipv4.conf.peg1.rp_filter=0",
	})
}

// TestDeviceVanishingLeavesTheTableContained is the fail-closed invariant: the next
// pass must not read a kernel-purged front as a reason to drop the containment.
func TestDeviceVanishingLeavesTheTableContained(t *testing.T) {
	k := upHost(t)
	m := newManager(t, k)
	mustEnsure(t, m, row())

	k.DelLink("peg1")
	k.ResetOps()
	mustEnsure(t, m, row())

	if writes := k.Writes(); writes != 0 {
		t.Fatalf("a pass with the front gone issued %d writes: %v", writes, k.Ops())
	}
	assertList(t, "rules", k.Rules(), steadyRules)
	assertList(t, "routes", k.Routes(), []string{
		"v4 table 30001 blackhole default metric 4096",
		"v6 table 30001 blackhole default metric 4096",
	})
}

func TestTeardownRemovesRulesFirst(t *testing.T) {
	k := upHost(t)
	m := newManager(t, k)
	mustEnsure(t, m, row())
	k.ResetOps()

	if err := m.Remove(context.Background(), 1); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	assertList(t, "teardown ops", k.Ops(), []string{
		"rule- v4 prio 31001 iif pwg3 lookup 30001",
		"rule- v6 prio 31001 iif pwg3 lookup 30001",
		"route- v4 table 30001 default dev peg1 metric 100",
		"route- v6 table 30001 default dev peg1 metric 100",
		"route- v4 table 30001 blackhole default metric 4096",
		"route- v6 table 30001 blackhole default metric 4096",
	})
	assertList(t, "rules", k.Rules(), nil)
	assertList(t, "routes", k.Routes(), nil)
}

func TestDisablingARowRemovesEverything(t *testing.T) {
	k := upHost(t)
	m := newManager(t, k)
	mustEnsure(t, m, row())

	off := row()
	off.Enable = false
	mustEnsure(t, m, off)
	assertList(t, "rules", k.Rules(), nil)
	assertList(t, "routes", k.Routes(), nil)
}

// TestReconcileDeletesStrandedResources is the case no cached fingerprint reaches:
// the row went away while the panel was down, so only the host still knows.
func TestReconcileDeletesStrandedResources(t *testing.T) {
	k := upHost(t)
	seedSteadyState(t, k)
	k.SetSysctlValue(rpFilterKey, "0")
	k.AddLink("peg5")
	k.SeedRule(
		egress.RuleSpec{Family: egress.FamilyV4, Priority: 31005, Iif: "pwg8", Table: 30005},
		egress.RuleSpec{Family: egress.FamilyV6, Priority: 31005, Iif: "pwg8", Table: 30005},
	)
	k.SeedRoute(
		egress.RouteSpec{Family: egress.FamilyV4, Table: 30005, Type: egress.RouteBlackhole, Dst: egress.FamilyV4.DefaultRoute(), Metric: 4096},
		egress.RouteSpec{Family: egress.FamilyV4, Table: 30005, Type: egress.RouteUnicast, Dst: egress.FamilyV4.DefaultRoute(), Device: "peg5", Metric: 100},
	)

	m := newManager(t, k)
	if err := m.Reconcile(context.Background(), []egress.Egress{row()}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	assertList(t, "stranded teardown ops", k.Ops(), []string{
		"rule- v4 prio 31005 iif pwg8 lookup 30005",
		"rule- v6 prio 31005 iif pwg8 lookup 30005",
		"route- v4 table 30005 default dev peg5 metric 100",
		"route- v4 table 30005 blackhole default metric 4096",
	})
	assertList(t, "the surviving row", k.Rules(), steadyRules)
	assertList(t, "the surviving table", k.Routes(), steadyRoutes)
}

// TestForeignObjectsAreNeverTouched is the other half of the ownership rule. A
// deleting reconciler that guesses is worse than one that leaves drift behind.
func TestForeignObjectsAreNeverTouched(t *testing.T) {
	foreignRule := egress.RuleSpec{Family: egress.FamilyV4, Priority: 31005, Iif: "pwg9", Table: 30777}
	foreignRoute := egress.RouteSpec{Family: egress.FamilyV4, Table: 30005, Type: egress.RouteUnicast, Dst: mustPrefix(t, "10.0.0.0/8"), Device: "eth0"}
	nearMiss := egress.RouteSpec{Family: egress.FamilyV4, Table: 30007, Type: egress.RouteUnicast, Dst: egress.FamilyV4.DefaultRoute(), Device: "peg007", Metric: 100}
	prohibit := egress.RouteSpec{Family: egress.FamilyV4, Table: 30009, Type: egress.RouteOther, Dst: mustPrefix(t, "192.168.77.0/24")}

	k := egtest.New()
	k.AddLink("eth0")
	k.AddLink("peg007")
	k.SeedRule(foreignRule)
	k.SeedRoute(foreignRoute, nearMiss, prohibit)

	m := newManager(t, k)
	if err := m.Reconcile(context.Background(), nil); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if writes := k.Writes(); writes != 0 {
		t.Fatalf("reconcile touched %d foreign objects: %v", writes, k.Ops())
	}
	assertList(t, "rules", k.Rules(), []string{"v4 prio 31005 iif pwg9 lookup 30777"})
	assertList(t, "routes", k.Routes(), []string{
		"v4 table 30005 10.0.0.0/8 dev eth0 metric 0",
		"v4 table 30007 default dev peg007 metric 100",
		"v4 table 30009 192.168.77.0/24 dev  metric 0",
	})
}

// TestBlackholeFailureWithholdsTheRule is the partial-apply rule that matters:
// selecting a table the panel could not contain is how an egress leaks.
func TestBlackholeFailureWithholdsTheRule(t *testing.T) {
	k := upHost(t)
	boom := errors.New("netlink says no")
	k.Fail = map[string]error{"route+ v4 table 30001 blackhole default metric 4096": boom}

	m := newManager(t, k)
	err := m.Ensure(context.Background(), row())
	if err == nil {
		t.Fatal("Ensure succeeded with the v4 table uncontained")
	}
	if !strings.Contains(err.Error(), "refusing v4 prio 31001 iif pwg3 lookup 30001: its table has no blackhole") {
		t.Fatalf("error = %v, want it to name the withheld rule and why", err)
	}
	assertList(t, "rules", k.Rules(), []string{"v6 prio 31001 iif pwg3 lookup 30001"})
	assertList(t, "routes", k.Routes(), []string{
		"v4 table 30001 default dev peg1 metric 100",
		"v6 table 30001 blackhole default metric 4096",
		"v6 table 30001 default dev peg1 metric 100",
	})
}

// TestRuleFailureKeepsTheBlackhole is the same invariant on the way out: while a
// rule still points at the table, removing its blackhole opens the leak.
func TestRuleFailureKeepsTheBlackhole(t *testing.T) {
	k := upHost(t)
	m := newManager(t, k)
	mustEnsure(t, m, row())

	k.Fail = map[string]error{"rule- v4 prio 31001 iif pwg3 lookup 30001": errors.New("netlink says no")}
	k.ResetOps()
	err := m.Remove(context.Background(), 1)
	if err == nil {
		t.Fatal("Remove succeeded with a rule still installed")
	}
	if !strings.Contains(err.Error(), "keeping v4 table 30001 blackhole default metric 4096: a rule still points at its table") {
		t.Fatalf("error = %v, want it to name the blackhole it kept and why", err)
	}
	assertList(t, "rules", k.Rules(), []string{"v4 prio 31001 iif pwg3 lookup 30001"})
	assertList(t, "routes", k.Routes(), []string{"v4 table 30001 blackhole default metric 4096"})
}

// TestAnOccupiedFrontSlotIsNotSilent is why an add answered EEXIST is a failure and
// a delete answered ESRCH is not: the egress is dark and has to say so.
func TestAnOccupiedFrontSlotIsNotSilent(t *testing.T) {
	k := upHost(t)
	k.AddLink("eth0")
	squatter := egress.RouteSpec{
		Family: egress.FamilyV4, Table: 30001, Type: egress.RouteUnicast,
		Dst: egress.FamilyV4.DefaultRoute(), Device: "eth0", Metric: 100,
	}
	k.SeedRoute(squatter)

	m := newManager(t, k)
	err := m.Ensure(context.Background(), row())
	if !errors.Is(err, egress.ErrAlreadyInstalled) {
		t.Fatalf("Ensure = %v, want ErrAlreadyInstalled naming the occupied slot", err)
	}
	if !strings.Contains(err.Error(), "route v4 table 30001 default dev peg1 metric 100") {
		t.Fatalf("error = %v, want it to name the front it could not install", err)
	}
	// Contained, not released: the rules and the blackhole went in regardless.
	assertList(t, "rules", k.Rules(), steadyRules)
	if indexOf(k.Routes(), "v4 table 30001 default dev eth0 metric 100") < 0 {
		t.Fatalf("the foreign route was touched: %v", k.Routes())
	}
}

// TestTheStandInAnswersLikeTheKernel certifies the fake against what was
// measured on 6.8. Every convergence test above is only worth what this is.
func TestTheStandInAnswersLikeTheKernel(t *testing.T) {
	ctx := context.Background()
	rule := egress.RuleSpec{Family: egress.FamilyV4, Priority: 31001, Iif: "pwg3", Table: 30001}
	sameSlot := egress.RouteSpec{
		Family: egress.FamilyV4, Table: 30001, Type: egress.RouteUnicast,
		Dst: egress.FamilyV4.DefaultRoute(), Device: "eth0", Metric: 100,
	}

	t.Run("an exact duplicate rule is refused", func(t *testing.T) {
		k := egtest.New()
		if err := k.AddRule(ctx, rule); err != nil {
			t.Fatalf("first add: %v", err)
		}
		if err := k.AddRule(ctx, rule); !errors.Is(err, egress.ErrAlreadyInstalled) {
			t.Fatalf("second add = %v, want ErrAlreadyInstalled", err)
		}
	})

	t.Run("one priority holds two devices", func(t *testing.T) {
		k := egtest.New()
		other := rule
		other.Iif = "pwg7"
		if err := k.AddRule(ctx, rule); err != nil {
			t.Fatalf("first add: %v", err)
		}
		if err := k.AddRule(ctx, other); err != nil {
			t.Fatalf("second device at one priority: %v", err)
		}
		assertList(t, "rules", k.Rules(), []string{
			"v4 prio 31001 iif pwg3 lookup 30001",
			"v4 prio 31001 iif pwg7 lookup 30001",
		})
	})

	t.Run("a missing rule cannot be deleted", func(t *testing.T) {
		k := egtest.New()
		if err := k.DelRule(ctx, rule); !errors.Is(err, egress.ErrNotInstalled) {
			t.Fatalf("del = %v, want ErrNotInstalled", err)
		}
	})

	t.Run("one table holds a front and a blackhole", func(t *testing.T) {
		k := egtest.New()
		k.AddLink("peg1")
		if err := k.AddRoute(ctx, blackhole(egress.FamilyV4)); err != nil {
			t.Fatalf("blackhole: %v", err)
		}
		if err := k.AddRoute(ctx, front(egress.FamilyV4, "peg1")); err != nil {
			t.Fatalf("front: %v", err)
		}
	})

	t.Run("one metric is one slot whatever the device", func(t *testing.T) {
		k := egtest.New()
		k.AddLink("peg1")
		k.AddLink("eth0")
		if err := k.AddRoute(ctx, front(egress.FamilyV4, "peg1")); err != nil {
			t.Fatalf("front: %v", err)
		}
		if err := k.AddRoute(ctx, sameSlot); !errors.Is(err, egress.ErrAlreadyInstalled) {
			t.Fatalf("second route at one metric = %v, want ErrAlreadyInstalled", err)
		}
	})

	t.Run("a route through an absent device is refused", func(t *testing.T) {
		k := egtest.New()
		if err := k.AddRoute(ctx, front(egress.FamilyV4, "peg1")); !errors.Is(err, egress.ErrNoDevice) {
			t.Fatalf("front without its device = %v, want ErrNoDevice", err)
		}
	})

	t.Run("deleting a device purges its routes and keeps the rules", func(t *testing.T) {
		k := egtest.New()
		k.AddLink("peg1", rpFilterKey)
		if err := k.AddRoute(ctx, blackhole(egress.FamilyV4)); err != nil {
			t.Fatalf("blackhole: %v", err)
		}
		if err := k.AddRoute(ctx, front(egress.FamilyV4, "peg1")); err != nil {
			t.Fatalf("front: %v", err)
		}
		if err := k.AddRule(ctx, rule); err != nil {
			t.Fatalf("rule: %v", err)
		}
		k.DelLink("peg1")
		assertList(t, "routes", k.Routes(), []string{"v4 table 30001 blackhole default metric 4096"})
		assertList(t, "rules", k.Rules(), []string{"v4 prio 31001 iif pwg3 lookup 30001"})
		if _, err := k.Sysctl(ctx, rpFilterKey); !errors.Is(err, egress.ErrNoDevice) {
			t.Fatalf("knob after the device went = %v, want ErrNoDevice", err)
		}
	})
}

func TestUnknownDriverContainsRatherThanReleases(t *testing.T) {
	k := upHost(t)
	m := newManager(t, k)
	unknown := row()
	unknown.Type = "ikev2"

	err := m.Ensure(context.Background(), unknown)
	if !errors.Is(err, egress.ErrUnknownDriver) {
		t.Fatalf("Ensure = %v, want ErrUnknownDriver", err)
	}
	assertList(t, "rules", k.Rules(), steadyRules)
	assertList(t, "routes", k.Routes(), []string{
		"v4 table 30001 blackhole default metric 4096",
		"v6 table 30001 blackhole default metric 4096",
	})
}

// TestDriverNamesItsOwnDevice proves the plane never learns what a device is:
// a driver whose front is made by something other than Xray changes nothing.
func TestDriverNamesItsOwnDevice(t *testing.T) {
	k := egtest.New()
	k.AddLink("ipsec1")
	m := newManager(t, k, namedDriver{kind: "ikev2", device: "ipsec1"})

	strongswan := row()
	strongswan.Type = "ikev2"
	mustEnsure(t, m, strongswan)
	assertList(t, "routes", k.Routes(), []string{
		"v4 table 30001 blackhole default metric 4096",
		"v4 table 30001 default dev ipsec1 metric 100",
		"v6 table 30001 blackhole default metric 4096",
		"v6 table 30001 default dev ipsec1 metric 100",
	})

	k.ResetOps()
	mustEnsure(t, m, strongswan)
	if writes := k.Writes(); writes != 0 {
		t.Fatalf("a converged foreign-device pass issued %d writes: %v", writes, k.Ops())
	}
}

func TestFillFailureStillContains(t *testing.T) {
	k := egtest.New()
	broken := errors.New("strongswan is not installed")
	m := newManager(t, k, namedDriver{kind: "ikev2", err: broken})

	e := row()
	e.Type = "ikev2"
	err := m.Ensure(context.Background(), e)
	if !errors.Is(err, broken) {
		t.Fatalf("Ensure = %v, want the driver's own error", err)
	}
	assertList(t, "rules", k.Rules(), steadyRules)
	assertList(t, "routes", k.Routes(), []string{
		"v4 table 30001 blackhole default metric 4096",
		"v6 table 30001 blackhole default metric 4096",
	})
}

func TestIDOutsideTheBandIsRefused(t *testing.T) {
	for _, id := range []int{0, 1000, -1} {
		k := egtest.New()
		m := newManager(t, k)
		e := row()
		e.ID = id
		if err := m.Ensure(context.Background(), e); !errors.Is(err, egress.ErrIDOutOfRange) {
			t.Fatalf("Ensure(id=%d) = %v, want ErrIDOutOfRange", id, err)
		}
		if writes := k.Writes(); writes != 0 {
			t.Fatalf("Ensure(id=%d) wrote %d objects outside the band: %v", id, writes, k.Ops())
		}
	}
}

// TestReconcileIsOrderIndependent keeps the op log stable: an unstable order
// makes every ordering assertion above a coin toss.
func TestReconcileIsOrderIndependent(t *testing.T) {
	build := func(rows []egress.Egress) []string {
		k := egtest.New()
		k.AddLink("peg1", rpFilterKey)
		k.AddLink("peg2", "net.ipv4.conf.peg2.rp_filter")
		m := newManager(t, k)
		if err := m.Reconcile(context.Background(), rows); err != nil {
			t.Fatalf("Reconcile: %v", err)
		}
		return k.Ops()
	}
	first := row()
	second := egress.Egress{ID: 2, Type: xraytun.Type, Enable: true, Ingress: []string{"pwg9", "pwg4"}}
	forward := build([]egress.Egress{first, second})
	backward := build([]egress.Egress{second, first})
	assertList(t, "reversed input", backward, forward)
}

// TestSnapshotFailureChangesNothing keeps a read failure from being read as "the
// host holds nothing", which would delete every object on the box.
func TestSnapshotFailureChangesNothing(t *testing.T) {
	k := upHost(t)
	m := newManager(t, k)
	mustEnsure(t, m, row())

	blind := errors.New("netlink socket is gone")
	k.SnapshotErr = blind
	k.ResetOps()
	if err := m.Reconcile(context.Background(), nil); !errors.Is(err, blind) {
		t.Fatalf("Reconcile = %v, want the snapshot error", err)
	}
	if writes := k.Writes(); writes != 0 {
		t.Fatalf("a blind pass issued %d writes: %v", writes, k.Ops())
	}
}

/*
A host that switched IPv6 off on new devices refuses the v6 front route forever,
and it is a benign permanent condition, not a failure to report.

The v6 rule and the v6 blackhole still install, so v6 is contained. Reporting it
would make every whole-host pass return an error on this box, and Attach reverts
on an error that names its own inbound — so one such host would end up unable to
attach anything at all.
*/
func TestFamilyDisabledOnTheFrontIsNotAFailure(t *testing.T) {
	k := upHost(t)
	k.Fail = map[string]error{
		"route+ v6 table 30001 default dev peg1 metric 100": fmt.Errorf("%w: peg1", egress.ErrFamilyDisabled),
	}
	m := newManager(t, k)

	if err := m.Reconcile(context.Background(), []egress.Egress{row()}); err != nil {
		t.Fatalf("Reconcile = %v, want nil: v6 is contained, and a permanent host fact must not poison every other row", err)
	}
	want := []string{
		"v4 table 30001 blackhole default metric 4096",
		"v4 table 30001 default dev peg1 metric 100",
		"v6 table 30001 blackhole default metric 4096",
	}
	assertList(t, "routes", k.Routes(), want)
	assertList(t, "rules", k.Rules(), steadyRules)
}

// Selects is what tells "my attachment landed" from "some other row on this host
// is unhappy" — the two a whole-host Reconcile joins into one error.
func TestSelectsAnswersPerIngressDevice(t *testing.T) {
	k := upHost(t)
	m := newManager(t, k)
	mustEnsure(t, m, row())

	cases := []struct {
		name string
		iif  string
		id   int
		want bool
	}{
		{name: "the egress it was attached to", iif: "pwg3", id: 1, want: true},
		{name: "an egress it was never attached to", iif: "pwg3", id: 2},
		{name: "detached, while a rule still selects it", iif: "pwg3", id: 0},
		{name: "detached, with nothing selecting it", iif: "pwg9", id: 0, want: true},
		{name: "a device no rule mentions", iif: "pwg9", id: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := m.Selects(context.Background(), tc.iif, tc.id)
			if err != nil {
				t.Fatalf("Selects: %v", err)
			}
			if got != tc.want {
				t.Fatalf("Selects(%q, %d) = %v, want %v", tc.iif, tc.id, got, tc.want)
			}
		})
	}

	// One family routed and the other not is not an attachment: the unrouted half
	// leaves through main with the server's own address.
	drop(t, k, egress.RuleSpec{Family: egress.FamilyV6, Priority: 31001, Iif: "pwg3", Table: 30001})
	got, err := m.Selects(context.Background(), "pwg3", 1)
	if err != nil {
		t.Fatalf("Selects: %v", err)
	}
	if got {
		t.Fatal("Selects accepted a v4-only attachment, so every v6 flow from that inbound leaves via main")
	}
}

// TestRegistryRefusesADuplicateType keeps two drivers from claiming one type,
// where the winner would depend on registration order.
func TestRegistryRefusesADuplicateType(t *testing.T) {
	registry := egress.NewRegistry()
	if err := registry.Register(xraytun.New()); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := registry.Register(namedDriver{kind: xraytun.Type, device: "peg1"}); !errors.Is(err, egress.ErrDuplicateDriver) {
		t.Fatalf("second register = %v, want ErrDuplicateDriver", err)
	}
	if err := registry.Register(nil); err == nil {
		t.Fatal("Register(nil) succeeded")
	}
	if err := registry.Register(namedDriver{}); err == nil {
		t.Fatal("Register of an empty type succeeded")
	}
	assertList(t, "types", registry.Types(), []string{xraytun.Type})
	if _, known := registry.For("ikev2"); known {
		t.Fatal("an unregistered type resolved")
	}
}

// namedDriver stands in for a type whose front is created by something other
// than Xray, which is the whole point of the driver seam.
type namedDriver struct {
	kind   string
	device string
	err    error
}

func (d namedDriver) Type() string { return d.kind }

func (d namedDriver) Fill(egress.Egress) (egress.Fill, error) {
	if d.err != nil {
		return egress.Fill{}, d.err
	}
	return egress.Fill{Device: d.device}, nil
}

func blackhole(family egress.Family) egress.RouteSpec {
	return egress.RouteSpec{Family: family, Table: 30001, Type: egress.RouteBlackhole, Dst: family.DefaultRoute(), Metric: 4096}
}

func front(family egress.Family, device string) egress.RouteSpec {
	return egress.RouteSpec{Family: family, Table: 30001, Type: egress.RouteUnicast, Dst: family.DefaultRoute(), Device: device, Metric: 100}
}

func seedSteadyState(t *testing.T, k *egtest.Kernel) {
	t.Helper()
	k.SeedRoute(blackhole(egress.FamilyV4), blackhole(egress.FamilyV6),
		front(egress.FamilyV4, "peg1"), front(egress.FamilyV6, "peg1"))
	k.SeedRule(
		egress.RuleSpec{Family: egress.FamilyV4, Priority: 31001, Iif: "pwg3", Table: 30001},
		egress.RuleSpec{Family: egress.FamilyV6, Priority: 31001, Iif: "pwg3", Table: 30001},
	)
}

func drop(t *testing.T, k *egtest.Kernel, specs ...egress.RuleSpec) {
	t.Helper()
	for _, spec := range specs {
		if err := k.DelRule(context.Background(), spec); err != nil {
			t.Fatalf("seed damage: %v", err)
		}
	}
	k.ResetOps()
}

func dropRoute(t *testing.T, k *egtest.Kernel, specs ...egress.RouteSpec) {
	t.Helper()
	for _, spec := range specs {
		if err := k.DelRoute(context.Background(), spec); err != nil {
			t.Fatalf("seed damage: %v", err)
		}
	}
	k.ResetOps()
}

func mustPrefix(t *testing.T, s string) netip.Prefix {
	t.Helper()
	prefix, err := netip.ParsePrefix(s)
	if err != nil {
		t.Fatalf("parse %q: %v", s, err)
	}
	return prefix
}

func indexOf(list []string, want string) int {
	for i, item := range list {
		if item == want {
			return i
		}
	}
	return -1
}
