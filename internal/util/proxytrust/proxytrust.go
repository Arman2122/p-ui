// Package proxytrust answers one question for every listener the panel runs:
// is this peer inside trustedProxyCIDRs, and so allowed to set X-Forwarded-*.
package proxytrust

import (
	"net"
	"net/netip"
	"strings"
)

// RemoteAddrInCIDRs reports whether remoteAddr (host or host:port) falls inside
// the comma-separated CIDR or address list; unparsable input never trusts.
func RemoteAddrInCIDRs(remoteAddr, cidrs string) bool {
	host := remoteAddr
	if h, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = h
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(host))
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	for value := range strings.SplitSeq(cidrs, ",") {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(value); err == nil {
			if prefix.Contains(addr) {
				return true
			}
			continue
		}
		if proxyIP, err := netip.ParseAddr(value); err == nil && proxyIP.Unmap() == addr {
			return true
		}
	}
	return false
}

// ForwardedProtoHTTPS reports whether a peer inside cidrs says the original
// request was HTTPS; an untrusted peer's header is ignored, not believed.
func ForwardedProtoHTTPS(remoteAddr, forwardedProto, cidrs string) bool {
	if !strings.EqualFold(strings.TrimSpace(forwardedProto), "https") {
		return false
	}
	cidrs = strings.TrimSpace(cidrs)
	if cidrs == "" {
		return false
	}
	return RemoteAddrInCIDRs(remoteAddr, cidrs)
}
