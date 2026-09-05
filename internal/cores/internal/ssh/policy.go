package ssh

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"time"
)

/*
Where a forward may go when the inbound dials for itself.

A proxy client names any address it likes and the server dialled it, so the
panel's database and API, the RADIUS server, the Xray gRPC port and the
sing-box control ports, all on loopback, were one direct-tcpip request away.
Xray answers the same question for a routed inbound with its private-address
block; this is the unrouted inbound's copy of that rule.
*/

var errDestinationRefused = errors.New("ssh: destination is not reachable through this inbound")

// reservedDestinations are the ranges no proxy client has business in: the
// host, its neighbours, and what a router never forwards.
var reservedDestinations = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.0.0.0/24"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("198.18.0.0/15"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

func destinationRefused(addr netip.Addr) error {
	addr = addr.Unmap()
	for _, p := range reservedDestinations {
		if p.Contains(addr) {
			return fmt.Errorf("%w: %s is a private or local address", errDestinationRefused, addr)
		}
	}
	if _, own := ownAddresses()[addr]; own {
		return fmt.Errorf("%w: %s is this server's own address", errDestinationRefused, addr)
	}
	return nil
}

// ownAddresses is every address on a local interface, refreshed at most every
// few seconds: a public address of the box is as much "the host" as loopback.
func ownAddresses() map[netip.Addr]struct{} {
	own.mu.Lock()
	defer own.mu.Unlock()
	if own.set != nil && time.Since(own.at) < ownAddressesTTL {
		return own.set
	}
	set := map[netip.Addr]struct{}{}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if addr, ok := netip.AddrFromSlice(ip); ok {
				set[addr.Unmap()] = struct{}{}
			}
		}
	}
	own.at, own.set = time.Now(), set
	return set
}

const ownAddressesTTL = 10 * time.Second

var own struct {
	mu  sync.Mutex
	at  time.Time
	set map[netip.Addr]struct{}
}

/*
guardedDialer resolves the destination itself and checks every address before
dialling one, so the check is on what would be connected to and not on what
was typed: a name that resolves to loopback is refused, and a name with one
reserved answer among several is refused whole rather than raced.
*/
func guardedDialer(next Dialer) Dialer {
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		addrs, err := resolveDestination(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, addr := range addrs {
			if err := destinationRefused(addr); err != nil {
				return nil, err
			}
		}
		var lastErr error
		for _, addr := range addrs {
			conn, err := next(ctx, network, net.JoinHostPort(addr.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
		}
		return nil, lastErr
	}
}

func resolveDestination(ctx context.Context, host string) ([]netip.Addr, error) {
	if addr, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{addr}, nil
	}
	addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("ssh: %s has no address", host)
	}
	return addrs, nil
}

// directDialer is what an unrouted inbound dials with: straight out, under the
// destination policy above.
func directDialer() Dialer {
	return guardedDialer((&net.Dialer{Timeout: dialTimeout}).DialContext)
}
