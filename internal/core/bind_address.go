package core

import "net"

// BindAddress describes the address families a TCP or UDP listener occupies.
type BindAddress struct {
	Listen string
	V6Only bool
}

func BindListenOverlaps(a, b BindAddress) bool {
	if a.Listen == b.Listen {
		return true
	}
	ipA, ipB := net.ParseIP(a.Listen), net.ParseIP(b.Listen)
	if ipA != nil && ipB != nil && ipA.Equal(ipB) {
		return true
	}
	familiesA, wildcardA := bindFamilies(a)
	familiesB, wildcardB := bindFamilies(b)
	if familiesA == 0 || familiesB == 0 {
		return wildcardA || wildcardB
	}
	return (wildcardA || wildcardB) && familiesA&familiesB != 0
}

// Go's TCP/UDP wildcard listeners cover both families unless IPV6_V6ONLY is set.
func bindFamilies(address BindAddress) (uint8, bool) {
	const ipv4, ipv6 = 1, 2
	ip := net.ParseIP(address.Listen)
	if address.Listen == "" || (ip != nil && ip.IsUnspecified()) {
		if address.V6Only {
			return ipv6, true
		}
		return ipv4 | ipv6, true
	}
	if ip == nil {
		return 0, false
	}
	if ip.To4() != nil {
		return ipv4, false
	}
	return ipv6, false
}
