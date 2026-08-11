package egress

import (
	"errors"
	"net/netip"
	"testing"
)

func TestDerivedResources(t *testing.T) {
	cases := []struct {
		name    string
		id      int
		table   int
		prio    int
		device  string
		gateway string
	}{
		{name: "first", id: 1, table: 30001, prio: 31001, device: "peg1", gateway: "100.127.0.1/32"},
		{name: "second", id: 2, table: 30002, prio: 31002, device: "peg2", gateway: "100.127.0.2/32"},
		{name: "octet boundary", id: 256, table: 30256, prio: 31256, device: "peg256", gateway: "100.127.1.0/32"},
		{name: "last", id: 999, table: 30999, prio: 31999, device: "peg999", gateway: "100.127.3.231/32"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Table(tc.id); got != tc.table {
				t.Errorf("Table(%d) = %d, want %d", tc.id, got, tc.table)
			}
			if got := Prio(tc.id); got != tc.prio {
				t.Errorf("Prio(%d) = %d, want %d", tc.id, got, tc.prio)
			}
			if got := Device(tc.id); got != tc.device {
				t.Errorf("Device(%d) = %q, want %q", tc.id, got, tc.device)
			}
			// IFNAMSIZ is 16 including the terminator; a longer name is silently
			// truncated by the kernel and then never matches its own route.
			if len(tc.device) > 15 {
				t.Errorf("Device(%d) = %q is %d chars, the kernel takes 15", tc.id, tc.device, len(tc.device))
			}
			gateway, err := Gateway(DefaultGatewayBase, tc.id)
			if err != nil {
				t.Fatalf("Gateway(%d) failed: %v", tc.id, err)
			}
			if gateway.String() != tc.gateway {
				t.Errorf("Gateway(%d) = %s, want %s", tc.id, gateway, tc.gateway)
			}
		})
	}
}

func TestIDBand(t *testing.T) {
	cases := []struct {
		name  string
		id    int
		valid bool
	}{
		{name: "below the band", id: 0, valid: false},
		{name: "negative", id: -1, valid: false},
		{name: "first", id: 1, valid: true},
		{name: "last", id: 999, valid: true},
		{name: "above the band", id: 1000, valid: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ValidID(tc.id); got != tc.valid {
				t.Errorf("ValidID(%d) = %v, want %v", tc.id, got, tc.valid)
			}
			err := checkID(tc.id)
			if tc.valid && err != nil {
				t.Fatalf("checkID(%d) = %v, want nil", tc.id, err)
			}
			if !tc.valid && !errors.Is(err, ErrIDOutOfRange) {
				t.Fatalf("checkID(%d) = %v, want ErrIDOutOfRange", tc.id, err)
			}
		})
	}
}

func TestOwnedEgressIDRoundTripsOnly(t *testing.T) {
	cases := []struct {
		name  string
		input string
		id    int
		mine  bool
	}{
		{name: "own device", input: "peg1", id: 1, mine: true},
		{name: "own device at the top of the band", input: "peg999", id: 999, mine: true},
		{name: "zero padded is somebody else", input: "peg007", mine: false},
		{name: "zero is somebody else", input: "peg0", mine: false},
		{name: "out of band is somebody else", input: "peg1000", mine: false},
		{name: "not a number", input: "pegX", mine: false},
		{name: "bare prefix", input: "peg", mine: false},
		{name: "a wireguard device", input: "pwg1", mine: false},
		{name: "signed", input: "peg+1", mine: false},
		{name: "empty", input: "", mine: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, mine := ownedEgressID(tc.input)
			if mine != tc.mine {
				t.Fatalf("ownedEgressID(%q) claimed = %v, want %v", tc.input, mine, tc.mine)
			}
			if mine && id != tc.id {
				t.Errorf("ownedEgressID(%q) = %d, want %d", tc.input, id, tc.id)
			}
		})
	}
}

func TestBandsRoundTrip(t *testing.T) {
	cases := []struct {
		name  string
		table int
		prio  int
		id    int
		mine  bool
	}{
		{name: "first", table: 30001, prio: 31001, id: 1, mine: true},
		{name: "last", table: 30999, prio: 31999, id: 999, mine: true},
		{name: "the base itself is not an egress", table: 30000, prio: 31000, mine: false},
		{name: "above the band", table: 31000, prio: 32000, mine: false},
		{name: "main", table: 254, prio: 32766, mine: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, mine := tableEgressID(tc.table)
			if mine != tc.mine || (mine && id != tc.id) {
				t.Errorf("tableEgressID(%d) = %d,%v want %d,%v", tc.table, id, mine, tc.id, tc.mine)
			}
			id, mine = prioEgressID(tc.prio)
			if mine != tc.mine || (mine && id != tc.id) {
				t.Errorf("prioEgressID(%d) = %d,%v want %d,%v", tc.prio, id, mine, tc.id, tc.mine)
			}
		})
	}
}

// TestGatewaysAreUniquePerID is the property the whole address scheme exists
// for: two fronts sharing a /32 would answer for each other's return traffic.
func TestGatewaysAreUniquePerID(t *testing.T) {
	seen := make(map[netip.Prefix]int, MaxID)
	for id := MinID; id <= MaxID; id++ {
		gateway, err := Gateway(DefaultGatewayBase, id)
		if err != nil {
			t.Fatalf("Gateway(%d) failed: %v", id, err)
		}
		if gateway.Bits() != gateway.Addr().BitLen() {
			t.Fatalf("Gateway(%d) = %s, want a single host prefix", id, gateway)
		}
		if other, clash := seen[gateway]; clash {
			t.Fatalf("egress %d and %d both derive %s", other, id, gateway)
		}
		seen[gateway] = id
	}
	if len(seen) != MaxID {
		t.Fatalf("derived %d gateways for %d ids", len(seen), MaxID)
	}
}

func TestGatewayBase(t *testing.T) {
	cases := []struct {
		name    string
		base    string
		id      int
		want    string
		wantErr error
	}{
		{name: "a v6 base offsets the same way", base: "fd00:pe::/64", id: 5, wantErr: ErrGatewayBase},
		{name: "a ula base", base: "fd00:0:0:1::/64", id: 5, want: "fd00:0:0:1::5/128"},
		{name: "an unset base", base: "", id: 1, wantErr: ErrGatewayBase},
		{name: "a base too small for the id", base: "100.127.0.0/24", id: 999, wantErr: ErrGatewayBase},
		{name: "a base wide enough for the id", base: "100.127.0.0/24", id: 200, want: "100.127.0.200/32"},
		{name: "an id outside the band", base: "100.127.0.0/16", id: 1000, wantErr: ErrIDOutOfRange},
		{name: "an unmasked base is masked first", base: "100.127.9.9/16", id: 1, want: "100.127.0.1/32"},
		{name: "a base that overflows its family", base: "255.255.255.0/24", id: 999, wantErr: ErrGatewayBase},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base, _ := netip.ParsePrefix(tc.base)
			got, err := Gateway(base, tc.id)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Gateway(%q, %d) = %v, want %v", tc.base, tc.id, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Gateway(%q, %d) failed: %v", tc.base, tc.id, err)
			}
			if got.String() != tc.want {
				t.Errorf("Gateway(%q, %d) = %s, want %s", tc.base, tc.id, got, tc.want)
			}
		})
	}
}

// TestCheckGatewayBaseCoversTheWholeBand pins that a base is judged on the last
// id, not the one in hand: a /24 accepted today refuses egress 256 next year.
func TestCheckGatewayBaseCoversTheWholeBand(t *testing.T) {
	if err := CheckGatewayBase(netip.MustParsePrefix("100.127.0.0/24")); !errors.Is(err, ErrGatewayBase) {
		t.Fatalf("CheckGatewayBase(/24) = %v, want ErrGatewayBase", err)
	}
	if err := CheckGatewayBase(DefaultGatewayBase); err != nil {
		t.Fatalf("CheckGatewayBase(default) = %v, want nil", err)
	}
}

// TestDiffCountsRatherThanSets is what lets a surplus object be removed. A set
// diff would see the wanted copy present and leave the duplicate behind forever.
func TestDiffCountsRatherThanSets(t *testing.T) {
	cases := []struct {
		name string
		want []string
		have []string
		add  []string
		del  []string
	}{
		{name: "nothing to do", want: []string{"a"}, have: []string{"a"}},
		{name: "adopt what is already right", want: []string{"a", "b"}, have: []string{"b", "a"}},
		{name: "add what is missing", want: []string{"a", "b"}, have: []string{"a"}, add: []string{"b"}},
		{name: "delete what is unwanted", want: []string{"a"}, have: []string{"a", "b"}, del: []string{"b"}},
		{name: "delete a surplus copy", want: []string{"a"}, have: []string{"a", "a"}, del: []string{"a"}},
		{name: "delete every copy when unwanted", have: []string{"a", "a"}, del: []string{"a", "a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			add, del := diff(tc.want, tc.have)
			if !equalStrings(add, tc.add) {
				t.Errorf("add = %v, want %v", add, tc.add)
			}
			if !equalStrings(del, tc.del) {
				t.Errorf("del = %v, want %v", del, tc.del)
			}
		})
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
