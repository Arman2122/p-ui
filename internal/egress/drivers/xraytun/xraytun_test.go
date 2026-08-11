package xraytun_test

import (
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/egress"
	"github.com/Arman2122/p-ui/internal/egress/drivers/xraytun"
)

// TestInjectedFrontIsByteExact pins the inbound against the shape proven on the
// box. Xray reads it verbatim, so a reordered or renamed key is a live failure.
func TestInjectedFrontIsByteExact(t *testing.T) {
	cases := []struct {
		name string
		base netip.Prefix
		id   int
		want string
	}{
		{
			name: "the first egress on the default base",
			id:   1,
			want: `{"listen":"127.0.0.1","port":0,"protocol":"tun","tag":"peg1","settings":{"name":"peg1","mtu":1420,"gateway":["100.127.0.1/32"]}}`,
		},
		{
			name: "the last egress on the default base",
			id:   999,
			want: `{"listen":"127.0.0.1","port":0,"protocol":"tun","tag":"peg999","settings":{"name":"peg999","mtu":1420,"gateway":["100.127.3.231/32"]}}`,
		},
		{
			name: "an operator's own base",
			base: netip.MustParsePrefix("100.64.9.0/24"),
			id:   7,
			want: `{"listen":"127.0.0.1","port":0,"protocol":"tun","tag":"peg7","settings":{"name":"peg7","mtu":1420,"gateway":["100.64.9.7/32"]}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			driver := xraytun.New()
			if tc.base.IsValid() {
				driver.GatewayBase = tc.base
			}
			injection, err := driver.Inject(egress.Egress{ID: tc.id, Type: xraytun.Type, Enable: true})
			if err != nil {
				t.Fatalf("Inject(%d): %v", tc.id, err)
			}
			if got := string(injection.Inbound); got != tc.want {
				t.Fatalf("inbound =\n%s\nwant\n%s", got, tc.want)
			}
			if injection.Tag != egress.Device(tc.id) {
				t.Fatalf("tag = %q, want %q", injection.Tag, egress.Device(tc.id))
			}
		})
	}
}

/*
TestFrontNeverAsksXrayToRouteForIt is the one prohibition this driver exists to
hold.

TunConfig.Build defaults autoOutboundsInterface to "auto" whenever
autoSystemRoutingTable is non-empty, which installs a process-global dialer
controller binding EVERY Xray outbound to the tun — a total outage plus a
routing loop. The panel installs the one route it needs itself.
*/
func TestFrontNeverAsksXrayToRouteForIt(t *testing.T) {
	injection, err := xraytun.New().Inject(egress.Egress{ID: 1, Type: xraytun.Type, Enable: true})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	for _, forbidden := range []string{"autoSystemRoutingTable", "autoOutboundsInterface", "sniffing", "policy"} {
		if strings.Contains(string(injection.Inbound), forbidden) {
			t.Fatalf("the front carries %q: %s", forbidden, injection.Inbound)
		}
	}
}

func TestFillNamesTheDeviceAndItsKnob(t *testing.T) {
	fill, err := xraytun.New().Fill(egress.Egress{ID: 12, Type: xraytun.Type, Enable: true})
	if err != nil {
		t.Fatalf("Fill: %v", err)
	}
	if fill.Device != "peg12" {
		t.Fatalf("device = %q, want peg12", fill.Device)
	}
	// The same /32 Inject puts on the front, so a device holding the name but not
	// the address is somebody else's and is never routed into.
	if got := fill.Addr.String(); got != "100.127.0.12/32" {
		t.Fatalf("addr = %q, want 100.127.0.12/32 — the front's own gateway is what proves the device is this driver's", got)
	}
	// Reverse-path filtering is v4-only: /proc has no ipv6 conf.<dev>.rp_filter,
	// so asking for one would fail every pass on a device that is perfectly fine.
	want := map[string]string{"net.ipv4.conf.peg12.rp_filter": "0"}
	if len(fill.Sysctls) != len(want) {
		t.Fatalf("sysctls = %v, want %v", fill.Sysctls, want)
	}
	for key, value := range want {
		if fill.Sysctls[key] != value {
			t.Fatalf("sysctls[%q] = %q, want %q", key, fill.Sysctls[key], value)
		}
	}
}

func TestIDOutsideTheBandIsRefused(t *testing.T) {
	for _, id := range []int{0, 1000, -1} {
		if _, err := xraytun.New().Fill(egress.Egress{ID: id}); !errors.Is(err, egress.ErrIDOutOfRange) {
			t.Errorf("Fill(%d) = %v, want ErrIDOutOfRange", id, err)
		}
		if _, err := xraytun.New().Inject(egress.Egress{ID: id}); !errors.Is(err, egress.ErrIDOutOfRange) {
			t.Errorf("Inject(%d) = %v, want ErrIDOutOfRange", id, err)
		}
	}
}

// TestAnUnusableBaseFallsBackRatherThanEmitsNothing keeps a zero-valued driver
// from emitting an addressless front, which fails only on the return path.
func TestAnUnusableBaseFallsBackRatherThanEmitsNothing(t *testing.T) {
	injection, err := xraytun.Driver{}.Inject(egress.Egress{ID: 1, Type: xraytun.Type, Enable: true})
	if err != nil {
		t.Fatalf("Inject: %v", err)
	}
	if !strings.Contains(string(injection.Inbound), `"gateway":["100.127.0.1/32"]`) {
		t.Fatalf("inbound = %s, want the default gateway base", injection.Inbound)
	}
}

func TestTypeIsTheStoredColumnValue(t *testing.T) {
	if xraytun.New().Type() != "xray-tun" {
		t.Fatalf("Type() = %q, want xray-tun", xraytun.New().Type())
	}
	if xraytun.MTU != 1420 {
		t.Fatalf("MTU = %d, want 1420", xraytun.MTU)
	}
}
