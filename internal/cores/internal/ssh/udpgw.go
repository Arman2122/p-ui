package ssh

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/time/rate"
)

/*
UDP through an SSH tunnel, the way the HTTP-Injector family does it: the app
opens one direct-tcpip forward to a "UDPGW" address on loopback and speaks
badvpn's udpgw protocol over it, one stream carrying many UDP conversations.
This is that server, in-process: nothing else listens on the host, and every
datagram is billed and throttled like a TCP forward.

Wire format (badvpn protocol/packetproto.h and udpgw_proto.h): a 16-bit
little-endian length, then flags (one byte), a connection id (16-bit
little-endian), the address (4 or 16 bytes of IP as they travel on the wire,
then a 16-bit network-order port) and the datagram. A reply carries the
address the client asked for, so it can match it to the conversation.
*/

const (
	udpgwFlagKeepalive = 1 << 0
	udpgwFlagRebind    = 1 << 1
	udpgwFlagDNS       = 1 << 2
	udpgwFlagIPv6      = 1 << 3

	udpgwDefaultPort = 7300
	udpgwMaxFrame    = 65535
	// udpgwMaxConns is badvpn's own per-client default; the least recently
	// used conversation makes room, exactly as the reference server does.
	udpgwMaxConns = 256
	udpgwIdle     = 2 * time.Minute
	udpgwSweep    = 30 * time.Second
)

var errUDPGWFrame = errors.New("ssh: udpgw: malformed frame")

// udpDialer opens one UDP conversation to dst. The direct one is the guarded
// dialer with network udp; a bridged one is a SOCKS5 UDP association.
type udpDialer func(ctx context.Context, dst netip.AddrPort) (net.Conn, error)

/*
isUDPGWTarget is where the apps are told to find the gateway: loopback, by
address or name, or any address of the host itself. The port is the inbound's.
*/
func isUDPGWTarget(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	if addr.IsLoopback() {
		return true
	}
	_, own := ownAddresses()[addr]
	return own
}

type udpgwSession struct {
	s    *server
	c    *conn
	ch   gossh.Channel
	dial udpDialer
	up   *atomic.Int64
	down *atomic.Int64
	lim  *limiterPair

	wmu sync.Mutex
	mu  sync.Mutex
	// conns is touched by the one reader goroutine, the pumps and the sweeper.
	conns map[uint16]*udpgwConn
	done  chan struct{}
}

type udpgwConn struct {
	conid    uint16
	orig     netip.AddrPort
	pc       net.Conn
	lastUsed atomic.Int64
}

// udpgw answers one UDPGW forward for the life of the channel.
func (s *server) udpgw(c *conn, nc gossh.NewChannel) {
	if s.udp == nil {
		_ = nc.Reject(gossh.Prohibited, "this inbound routes through Xray and has no bridge port yet; save it again")
		return
	}
	ch, reqs, err := nc.Accept()
	if err != nil {
		return
	}
	go gossh.DiscardRequests(reqs)
	up, down := s.countersFor(c.email)
	sess := &udpgwSession{
		s: s, c: c, ch: ch, dial: s.udp, up: up, down: down, lim: s.limiterFor(c.email),
		conns: map[uint16]*udpgwConn{}, done: make(chan struct{}),
	}
	sess.run()
}

func (sess *udpgwSession) run() {
	defer sess.close()
	go sess.sweep()
	r := bufio.NewReaderSize(sess.ch, 64*1024)
	head := make([]byte, 2)
	frame := make([]byte, udpgwMaxFrame)
	for {
		if _, err := io.ReadFull(r, head); err != nil {
			return
		}
		n := int(binary.LittleEndian.Uint16(head))
		if n < 3 {
			return
		}
		if _, err := io.ReadFull(r, frame[:n]); err != nil {
			return
		}
		if err := sess.handle(frame[:n]); err != nil {
			return
		}
	}
}

func (sess *udpgwSession) handle(frame []byte) error {
	flags := frame[0]
	conid := binary.LittleEndian.Uint16(frame[1:3])
	rest := frame[3:]
	if flags&udpgwFlagKeepalive != 0 {
		return nil
	}
	var orig netip.AddrPort
	switch {
	case flags&udpgwFlagIPv6 != 0:
		if len(rest) < 18 {
			return errUDPGWFrame
		}
		orig = netip.AddrPortFrom(netip.AddrFrom16([16]byte(rest[:16])), binary.BigEndian.Uint16(rest[16:18]))
		rest = rest[18:]
	default:
		if len(rest) < 6 {
			return errUDPGWFrame
		}
		orig = netip.AddrPortFrom(netip.AddrFrom4([4]byte(rest[:4])), binary.BigEndian.Uint16(rest[4:6]))
		rest = rest[6:]
	}
	payload := rest
	if !sess.allow(sess.lim.up, len(payload)) {
		return nil
	}
	sess.up.Add(int64(len(payload)))
	sess.c.lastSeen.Store(time.Now().UnixMilli())
	con, err := sess.connFor(conid, orig, flags)
	if err != nil {
		// A destination the policy refuses, or one nothing can reach: that
		// datagram is lost, as it would be on any network, and the tunnel lives on.
		return nil
	}
	_ = con.pc.SetWriteDeadline(time.Now().Add(time.Second))
	if _, err := con.pc.Write(payload); err != nil {
		sess.remove(con)
	}
	return nil
}

/*
connFor is badvpn's rule: a conversation is reused while the client keeps
naming the same destination for the id; a rebind flag or a new destination
under an old id closes it and starts over. A DNS-flagged datagram goes to
the host's own resolver, because the apps send those to whatever resolver
the phone happened to have, which is not reachable from here.
*/
func (sess *udpgwSession) connFor(conid uint16, orig netip.AddrPort, flags byte) (*udpgwConn, error) {
	sess.mu.Lock()
	con, ok := sess.conns[conid]
	if ok && (flags&udpgwFlagRebind != 0 || con.orig != orig) {
		delete(sess.conns, conid)
		_ = con.pc.Close()
		ok = false
	}
	if ok {
		con.lastUsed.Store(time.Now().UnixMilli())
		sess.mu.Unlock()
		return con, nil
	}
	if len(sess.conns) >= udpgwMaxConns {
		sess.evictOldestLocked()
	}
	sess.mu.Unlock()

	dst, dial := orig, sess.dial
	if flags&udpgwFlagDNS != 0 {
		if sess.s.settings.RouteThroughXray {
			dst, dial = routedResolverFor(orig), sess.dnsOverTCP
		} else if resolver, ok := hostResolver(); ok {
			dst, dial = resolver, resolverDialer
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	defer cancel()
	pc, err := dial(ctx, dst)
	if err != nil {
		return nil, err
	}
	con = &udpgwConn{conid: conid, orig: orig, pc: pc}
	con.lastUsed.Store(time.Now().UnixMilli())
	sess.mu.Lock()
	sess.conns[conid] = con
	sess.mu.Unlock()
	go sess.pump(con)
	return con, nil
}

// routedResolver answers a routed inbound's DNS when the app named a resolver
// no exit can reach (the phone's own); a public one the app named is kept.
var routedResolver = netip.MustParseAddrPort("1.1.1.1:53")

func routedResolverFor(orig netip.AddrPort) netip.AddrPort {
	addr := orig.Addr().Unmap()
	if addr.IsGlobalUnicast() && !addr.IsPrivate() && orig.Port() != 0 {
		return orig
	}
	return routedResolver
}

// dnsOverTCP carries one DNS conversation through the inbound's routed TCP
// dialer (RFC 7766 framing), so a query leaves exactly where a forward leaves.
func (sess *udpgwSession) dnsOverTCP(ctx context.Context, dst netip.AddrPort) (net.Conn, error) {
	raw, err := sess.s.dial(ctx, "tcp", dst.String())
	if err != nil {
		return nil, err
	}
	return &dnsTCPConn{Conn: raw, r: bufio.NewReader(raw)}, nil
}

// dnsTCPConn is a datagram conn over a TCP stream: each Write is one framed
// query, each Read one framed answer.
type dnsTCPConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *dnsTCPConn) Write(p []byte) (int, error) {
	if len(p) > 0xffff {
		return 0, errUDPGWFrame
	}
	frame := make([]byte, 2+len(p))
	binary.BigEndian.PutUint16(frame, uint16(len(p)))
	copy(frame[2:], p)
	if _, err := c.Conn.Write(frame); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *dnsTCPConn) Read(p []byte) (int, error) {
	var head [2]byte
	if _, err := io.ReadFull(c.r, head[:]); err != nil {
		return 0, err
	}
	n := int(binary.BigEndian.Uint16(head[:]))
	if n > len(p) {
		return 0, io.ErrShortBuffer
	}
	return io.ReadFull(c.r, p[:n])
}

func (sess *udpgwSession) evictOldestLocked() {
	var oldest *udpgwConn
	for _, con := range sess.conns {
		if oldest == nil || con.lastUsed.Load() < oldest.lastUsed.Load() {
			oldest = con
		}
	}
	if oldest != nil {
		delete(sess.conns, oldest.conid)
		_ = oldest.pc.Close()
	}
}

func (sess *udpgwSession) remove(con *udpgwConn) {
	sess.mu.Lock()
	if sess.conns[con.conid] == con {
		delete(sess.conns, con.conid)
	}
	sess.mu.Unlock()
	_ = con.pc.Close()
}

// pump carries one conversation's replies back into the tunnel.
func (sess *udpgwSession) pump(con *udpgwConn) {
	buf := make([]byte, udpgwMaxFrame)
	for {
		n, err := con.pc.Read(buf)
		if err != nil {
			sess.remove(con)
			return
		}
		con.lastUsed.Store(time.Now().UnixMilli())
		if !sess.allow(sess.lim.down, n) {
			continue
		}
		sess.down.Add(int64(n))
		sess.c.lastSeen.Store(time.Now().UnixMilli())
		if err := sess.reply(con, buf[:n]); err != nil {
			return
		}
	}
}

func (sess *udpgwSession) reply(con *udpgwConn, payload []byte) error {
	var flags byte
	addr := con.orig.Addr()
	var ip []byte
	if addr.Is4() {
		b := addr.As4()
		ip = b[:]
	} else {
		flags |= udpgwFlagIPv6
		b := addr.As16()
		ip = b[:]
	}
	n := 3 + len(ip) + 2 + len(payload)
	if n > udpgwMaxFrame {
		return nil
	}
	frame := make([]byte, 2+n)
	binary.LittleEndian.PutUint16(frame[0:2], uint16(n))
	frame[2] = flags
	binary.LittleEndian.PutUint16(frame[3:5], con.conid)
	copy(frame[5:], ip)
	binary.BigEndian.PutUint16(frame[5+len(ip):], con.orig.Port())
	copy(frame[7+len(ip):], payload)
	sess.wmu.Lock()
	defer sess.wmu.Unlock()
	_, err := sess.ch.Write(frame)
	return err
}

// allow is the rate limit for one datagram: waited for when it fits the
// burst, dropped when it never could, as a router would drop it.
func (sess *udpgwSession) allow(lim *rate.Limiter, n int) bool {
	if lim.Limit() == rate.Inf {
		return true
	}
	if n > lim.Burst() {
		return false
	}
	return lim.WaitN(context.Background(), n) == nil
}

func (sess *udpgwSession) sweep() {
	ticker := time.NewTicker(udpgwSweep)
	defer ticker.Stop()
	for {
		select {
		case <-sess.done:
			return
		case <-ticker.C:
		}
		cutoff := time.Now().Add(-udpgwIdle).UnixMilli()
		sess.mu.Lock()
		var stale []*udpgwConn
		for _, con := range sess.conns {
			if con.lastUsed.Load() < cutoff {
				stale = append(stale, con)
			}
		}
		sess.mu.Unlock()
		for _, con := range stale {
			sess.remove(con)
		}
	}
}

func (sess *udpgwSession) close() {
	close(sess.done)
	sess.mu.Lock()
	conns := make([]*udpgwConn, 0, len(sess.conns))
	for _, con := range sess.conns {
		conns = append(conns, con)
	}
	sess.conns = map[uint16]*udpgwConn{}
	sess.mu.Unlock()
	for _, con := range conns {
		_ = con.pc.Close()
	}
	_ = sess.ch.Close()
}

// resolverDialer reaches the host's own resolver, which may well be on
// loopback: the destination is the server's choice, not the client's.
func resolverDialer(ctx context.Context, dst netip.AddrPort) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "udp", dst.String())
}

var resolver struct {
	mu   sync.Mutex
	at   time.Time
	addr netip.AddrPort
	ok   bool
}

// hostResolver is the first nameserver in /etc/resolv.conf, re-read every
// minute; false where there is none, and the datagram then goes where the
// client addressed it.
func hostResolver() (netip.AddrPort, bool) {
	resolver.mu.Lock()
	defer resolver.mu.Unlock()
	if time.Since(resolver.at) < time.Minute {
		return resolver.addr, resolver.ok
	}
	resolver.at, resolver.ok = time.Now(), false
	body, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return resolver.addr, false
	}
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}
		if addr, err := netip.ParseAddr(strings.Split(fields[1], "%")[0]); err == nil {
			resolver.addr, resolver.ok = netip.AddrPortFrom(addr, 53), true
			return resolver.addr, true
		}
	}
	return resolver.addr, false
}
