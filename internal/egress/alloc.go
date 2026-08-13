/*
Package egress sends one L3 inbound's traffic out through a chosen egress by
policy routing, without the inbound's own core knowing an egress exists.

Every kernel object is a pure function of the egress id, so nothing derived is
ever stored: the id is the only allocation, and reusing one while its kernel
state may still exist is the bug this whole file exists to make impossible.
*/
package egress

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// The id band and the three host-global namespaces derived from it. The fwmark
// bands and the SOCKS port band of §5.3 are unbuilt: no driver consumes them.
const (
	MinID     = 1
	MaxID     = 999
	tableBase = 30000
	prioBase  = 31000

	// devicePrefix marks a device as this panel's egress front. Like wireguard's
	// "pwg" it is deliberately not a name an operator would reach for.
	devicePrefix = "peg"

	// uplinkPrefix marks a device this panel DIALS OUT through. Its own namespace
	// because a front and an uplink are opposite ends: one terminates traffic the
	// panel received, the other originates traffic the panel sends.
	uplinkPrefix = "pux"

	// markBase is the fwmark band §5.3 reserves. MaxID is 999, so an id fits the
	// low bits and never reaches the byte the band itself occupies.
	markBase = 0x0e000000
)

// ValidID reports whether id is inside the band §5.3 reserves for egresses.
func ValidID(id int) bool { return id >= MinID && id <= MaxID }

// checkID names the band in the error, because an id out of range is a caller
// bug that would otherwise surface as a route in somebody else's table.
func checkID(id int) error {
	if !ValidID(id) {
		return fmt.Errorf("%w: %d is outside %d..%d", ErrIDOutOfRange, id, MinID, MaxID)
	}
	return nil
}

// Table is the private routing table one egress's default route lives in.
func Table(id int) int { return tableBase + id }

// Prio is the ip rule priority every rule selecting this egress is installed at.
// Rules share it: one per attached inbound, all pointing at the same table.
func Prio(id int) int { return prioBase + id }

// Device is the front device the table's default route points at. A driver may
// name its own device instead; this is the default and xray-tun's answer.
func Device(id int) string { return devicePrefix + strconv.Itoa(id) }

// Uplink is the device an egress this panel dials leaves through. Unlike Device
// it is created here rather than by a core, so its name is ours to derive.
func Uplink(id int) string { return uplinkPrefix + strconv.Itoa(id) }

// ownedUplinkID round-trips through Uplink, so pux007 and pux0 are not ours.
func ownedUplinkID(name string) (int, bool) {
	rest, ok := strings.CutPrefix(name, uplinkPrefix)
	if !ok {
		return 0, false
	}
	id, err := strconv.Atoi(rest)
	if err != nil || !ValidID(id) || Uplink(id) != name {
		return 0, false
	}
	return id, true
}

/*
Mark is the fwmark a socket carries to leave through this egress.

The other half of selection. An ingress device names traffic the kernel
FORWARDS; a mark names traffic this host ORIGINATES, which is the only handle
an L7 core's own socket offers.
*/
func Mark(id int) uint32 { return markBase | uint32(id) }

// markEgressID reads the id back out of a mark. The ValidID bound is
// load-bearing: an id learned outside the band derives objects checkID then
// refuses to collect, so the sweep would error on every pass forever.
func markEgressID(mark uint32) (int, bool) {
	id := int(mark &^ markBase)
	if !ValidID(id) || Mark(id) != mark {
		return 0, false
	}
	return id, true
}

// ownedEgressID reads the id back out of a device name. It round-trips through
// Device, so a near miss like peg007 or peg0 is somebody else's device.
func ownedEgressID(name string) (int, bool) {
	rest, ok := strings.CutPrefix(name, devicePrefix)
	if !ok {
		return 0, false
	}
	id, err := strconv.Atoi(rest)
	if err != nil || !ValidID(id) || Device(id) != name {
		return 0, false
	}
	return id, true
}

// tableEgressID reads the id back out of a table number, so a stranded table can
// be traced to the row that is no longer there.
func tableEgressID(table int) (int, bool) {
	id := table - tableBase
	return id, ValidID(id)
}

// prioEgressID reads the id back out of a rule priority.
func prioEgressID(prio int) (int, bool) {
	id := prio - prioBase
	return id, ValidID(id)
}

// DefaultGatewayBase is where the front's own /32 is carved from. An addressless
// front fails reverse-path filtering, and only on the return path.
var DefaultGatewayBase = netip.MustParsePrefix("100.127.0.0/16")

// Gateway is the front's own address: one host prefix per id, so two fronts on
// one host can never share a /32 and answer for each other's return traffic.
func Gateway(base netip.Prefix, id int) (netip.Prefix, error) {
	if err := checkID(id); err != nil {
		return netip.Prefix{}, err
	}
	if !base.IsValid() {
		return netip.Prefix{}, fmt.Errorf("%w: %q is not an address prefix", ErrGatewayBase, base)
	}
	addr, ok := offsetAddr(base.Masked().Addr(), id)
	if !ok || !base.Contains(addr) {
		return netip.Prefix{}, fmt.Errorf("%w: %s cannot hold egress %d", ErrGatewayBase, base, id)
	}
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

// CheckGatewayBase reports whether base can hold the whole band. Checking only
// the id in hand would accept a /24 and refuse the 256th egress a year later.
func CheckGatewayBase(base netip.Prefix) error {
	_, err := Gateway(base, MaxID)
	return err
}

// offsetAddr is addr+n, in whichever family addr is. It works on the 16-byte
// form so one loop serves both, and reports the carry rather than wrapping.
func offsetAddr(addr netip.Addr, n int) (netip.Addr, bool) {
	if !addr.IsValid() || n < 0 {
		return netip.Addr{}, false
	}
	bytes := addr.As16()
	carry := uint64(n)
	for i := 15; i >= 0 && carry > 0; i-- {
		carry += uint64(bytes[i])
		bytes[i] = byte(carry)
		carry >>= 8
	}
	if carry > 0 {
		return netip.Addr{}, false
	}
	out := netip.AddrFrom16(bytes)
	if addr.Is4() {
		out = out.Unmap()
		// A v4 sum that ate into the ::ffff: prefix stops being v4-mapped, which
		// Unmap leaves as a v6 address rather than reporting an overflow.
		if !out.Is4() {
			return netip.Addr{}, false
		}
	}
	return out, true
}
