package egress_test

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/egress"
	"github.com/Arman2122/p-ui/internal/egress/drivers/xraytun"
	"github.com/Arman2122/p-ui/internal/egress/egtest"
)

// cleanHost is a box an egress can run on: loose reverse-path filtering,
// forwarding already on for the L3 core in both families, and nothing in the
// reserved band.
func cleanHost(t *testing.T) *egtest.Kernel {
	t.Helper()
	k := egtest.New()
	k.SetSysctlValue(egress.AllRPFilterKey, "2")
	k.SetSysctlValue(egress.IPForwardKey, "1")
	k.SetSysctlValue(egress.IPForward6Key, "1")
	return k
}

func TestPreflight(t *testing.T) {
	cases := []struct {
		name    string
		host    func(t *testing.T, k *egtest.Kernel)
		base    netip.Prefix
		refusal error
		names   string
		note    string
	}{
		{name: "a clean host", base: egress.DefaultGatewayBase},
		{
			name:    "strict reverse path filtering",
			host:    func(t *testing.T, k *egtest.Kernel) { t.Helper(); k.SetSysctlValue(egress.AllRPFilterKey, "1") },
			base:    egress.DefaultGatewayBase,
			refusal: egress.ErrStrictRPFilter,
			names:   "net.ipv4.conf.all.rp_filter is 1",
		},
		{
			name: "loose filtering is fine",
			host: func(t *testing.T, k *egtest.Kernel) { t.Helper(); k.SetSysctlValue(egress.AllRPFilterKey, "0") },
			base: egress.DefaultGatewayBase,
		},
		{
			name: "forwarding off is reported, not refused",
			host: func(t *testing.T, k *egtest.Kernel) { t.Helper(); k.SetSysctlValue(egress.IPForwardKey, "0") },
			base: egress.DefaultGatewayBase,
			note: "net.ipv4.ip_forward is 0",
		},
		{
			name: "a foreign rule in the priority band",
			host: func(t *testing.T, k *egtest.Kernel) {
				t.Helper()
				k.SeedRule(egress.RuleSpec{Family: egress.FamilyV4, Priority: 31005, Iif: "pwg9", Table: 30777})
			},
			base:    egress.DefaultGatewayBase,
			refusal: egress.ErrForeignResource,
			names:   "v4 prio 31005 iif pwg9 lookup 30777 sits in the reserved priority band",
		},
		{
			name: "a foreign route in a reserved table",
			host: func(t *testing.T, k *egtest.Kernel) {
				t.Helper()
				k.AddLink("eth0")
				k.SeedRoute(egress.RouteSpec{
					Family: egress.FamilyV4, Table: 30005, Type: egress.RouteUnicast,
					Dst: mustPrefix(t, "10.0.0.0/8"), Device: "eth0",
				})
			},
			base:    egress.DefaultGatewayBase,
			refusal: egress.ErrForeignResource,
			names:   "v4 table 30005 10.0.0.0/8 dev eth0 metric 0 sits in a reserved table",
		},
		{
			name: "the panel's own objects are not foreign",
			host: func(t *testing.T, k *egtest.Kernel) {
				t.Helper()
				k.AddLink("peg1")
				k.AddAddr("peg1", mustGateway(t, 1))
				seedSteadyState(t, k)
			},
			base: egress.DefaultGatewayBase,
		},
		{
			// Every enabled xray-tun egress puts this address on its front, so a
			// refusal here would be the panel refusing its own steady state.
			name: "a second front carrying the gateway its own id derives",
			host: func(t *testing.T, k *egtest.Kernel) {
				t.Helper()
				k.AddLink("peg1")
				k.AddLink("peg2")
				k.AddAddr("peg1", mustGateway(t, 1))
				k.AddAddr("peg2", mustGateway(t, 2))
			},
			base: egress.DefaultGatewayBase,
		},
		{
			name: "a squatter inside the base on a device the panel does not own",
			host: func(t *testing.T, k *egtest.Kernel) {
				t.Helper()
				k.AddLink("eth0")
				k.AddAddr("eth0", mustPrefix(t, "100.127.0.5/32"))
			},
			base:    egress.DefaultGatewayBase,
			refusal: egress.ErrGatewayBase,
			names:   "already carries 100.127.0.5/32 on eth0",
		},
		{
			name: "a front's own gateway held by somebody else's device",
			host: func(t *testing.T, k *egtest.Kernel) {
				t.Helper()
				k.AddLink("eth0")
				k.AddAddr("eth0", mustGateway(t, 1))
			},
			base:    egress.DefaultGatewayBase,
			refusal: egress.ErrGatewayBase,
			names:   "already carries 100.127.0.1/32 on eth0",
		},
		{
			name: "an address a front picked up that is not its gateway",
			host: func(t *testing.T, k *egtest.Kernel) {
				t.Helper()
				k.AddLink("peg1")
				k.AddAddr("peg1", mustPrefix(t, "100.127.9.9/32"))
			},
			base:    egress.DefaultGatewayBase,
			refusal: egress.ErrGatewayBase,
			names:   "already carries 100.127.9.9/32 on peg1",
		},
		{
			name: "a gateway base already on the box",
			host: func(t *testing.T, k *egtest.Kernel) {
				t.Helper()
				k.AddAddr("eth0", mustPrefix(t, "100.127.4.1/24"))
			},
			base:    egress.DefaultGatewayBase,
			refusal: egress.ErrGatewayBase,
			names:   "already carries 100.127.4.1/24",
		},
		{
			name:    "a gateway base too small for the band",
			base:    mustPrefix(t, "100.127.0.0/24"),
			refusal: egress.ErrGatewayBase,
			names:   "cannot hold egress 999",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			k := cleanHost(t)
			if tc.host != nil {
				tc.host(t, k)
			}
			report := egress.New(k, nil).Preflight(context.Background(), tc.base)

			if tc.refusal == nil {
				if !report.OK() {
					t.Fatalf("Preflight refused a usable host: %v", report.Err())
				}
			} else {
				if !errors.Is(report.Err(), tc.refusal) {
					t.Fatalf("Preflight = %v, want %v", report.Err(), tc.refusal)
				}
				if !strings.Contains(report.Err().Error(), tc.names) {
					t.Fatalf("Preflight = %v, want it to name %q", report.Err(), tc.names)
				}
			}
			if tc.note != "" && !strings.Contains(strings.Join(report.Notes, "\n"), tc.note) {
				t.Fatalf("notes = %v, want one naming %q", report.Notes, tc.note)
			}
		})
	}
}

// TestPreflightStopsAtAnUnusableHost keeps a probe failure from being reported
// as a clean band: the walk that follows it would have read nothing.
func TestPreflightStopsAtAnUnusableHost(t *testing.T) {
	k := cleanHost(t)
	k.ProbeErr = egress.ErrPermission
	report := egress.New(k, nil).Preflight(context.Background(), egress.DefaultGatewayBase)
	if !errors.Is(report.Err(), egress.ErrPermission) {
		t.Fatalf("Preflight = %v, want ErrPermission", report.Err())
	}
	if len(report.Refusals) != 1 {
		t.Fatalf("refusals = %v, want the probe failure alone", report.Refusals)
	}
}

// TestPreflightSurvivesAnUnreadableKnob: a host whose rp_filter cannot be read
// is a warning, not a refusal — the knob is absent on some container runtimes.
func TestPreflightSurvivesAnUnreadableKnob(t *testing.T) {
	k := egtest.New()
	report := egress.New(k, nil).Preflight(context.Background(), egress.DefaultGatewayBase)
	if !report.OK() {
		t.Fatalf("Preflight refused a host with no readable knobs: %v", report.Err())
	}
	if !strings.Contains(strings.Join(report.Notes, "\n"), egress.AllRPFilterKey) {
		t.Fatalf("notes = %v, want one naming %s", report.Notes, egress.AllRPFilterKey)
	}
}

/*
An enabled row whose front never came up reads as perfectly healthy everywhere
else: the row says enabled, Selects still answers "routed" because the rules are
there, and the traffic is contained by the blackhole. So preflight is the one
surface that can say it, and it says it as a note — the front belongs to the core
and is legitimately absent between a restart and the next reconcile.
*/
func TestPreflightNamesARowWhoseFrontIsNotOnThisHost(t *testing.T) {
	rows := []egress.Egress{
		{ID: 1, Type: xraytun.Type, Enable: true, Target: "direct"},
		{ID: 2, Type: xraytun.Type, Enable: true, Target: "direct"},
		{ID: 3, Type: xraytun.Type, Target: "direct"},
	}
	registry := egress.NewRegistry()
	if err := registry.Register(xraytun.New()); err != nil {
		t.Fatalf("register: %v", err)
	}

	k := cleanHost(t)
	k.AddLink(egress.Device(1))
	k.AddAddr(egress.Device(1), mustGateway(t, 1))
	// Present by name only, which is exactly what a squatter looks like.
	k.AddLink(egress.Device(2))

	report := egress.New(k, registry).Preflight(context.Background(), egress.DefaultGatewayBase, rows...)
	if !report.OK() {
		t.Fatalf("Preflight refused: %v — an absent front is normal, never a refusal", report.Err())
	}
	notes := strings.Join(report.Notes, "\n")
	if !strings.Contains(notes, "egress 2 has no front on this host yet: peg2") {
		t.Fatalf("notes = %v, want egress 2 named", report.Notes)
	}
	for _, quiet := range []string{"egress 1 has no front", "egress 3 has no front"} {
		if strings.Contains(notes, quiet) {
			t.Fatalf("notes = %v, want no note for a healthy front or a disabled row", report.Notes)
		}
	}
}

// The .conf the panel hands a client routes ::/0 into the tunnel, so a host that
// forwards v4 and not v6 drops every v6 flow and reports nothing.
func TestPreflightNamesBothForwardingKnobs(t *testing.T) {
	k := cleanHost(t)
	k.SetSysctlValue(egress.IPForwardKey, "1")
	k.SetSysctlValue(egress.IPForward6Key, "0")

	report := egress.New(k, nil).Preflight(context.Background(), egress.DefaultGatewayBase)
	if !report.OK() {
		t.Fatalf("Preflight refused: %v — forwarding is reported, never owned", report.Err())
	}
	notes := strings.Join(report.Notes, "\n")
	if !strings.Contains(notes, egress.IPForward6Key+" is 0") {
		t.Fatalf("notes = %v, want one naming %s", report.Notes, egress.IPForward6Key)
	}
	if strings.Contains(notes, egress.IPForwardKey+" is 0") {
		t.Fatalf("notes = %v, want nothing said about a knob that is on", report.Notes)
	}
}
