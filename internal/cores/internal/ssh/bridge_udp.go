package ssh

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"time"
)

/*
UDP through the Xray bridge: one SOCKS5 UDP association per conversation.

Per conversation rather than per session on purpose: a reply carries only the
remote address, so two conversations with the same remote (two DNS lookups to
one resolver) could not be told apart on a shared relay, and Xray would see
one UDP session where the client has two. The control connection lives as long
as the conversation, because the relay is torn down with it.
*/
func bridgeUDPDialer(port int) udpDialer {
	relay := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	return func(ctx context.Context, dst netip.AddrPort) (net.Conn, error) {
		ctrl, err := (&net.Dialer{}).DialContext(ctx, "tcp", relay)
		if err != nil {
			return nil, fmt.Errorf("ssh: bridge: %w", err)
		}
		relayAddr, err := associate(ctrl)
		if err != nil {
			_ = ctrl.Close()
			return nil, fmt.Errorf("ssh: bridge: udp associate: %w", err)
		}
		pc, err := net.DialUDP("udp", nil, net.UDPAddrFromAddrPort(relayAddr))
		if err != nil {
			_ = ctrl.Close()
			return nil, err
		}
		return &socksUDPConn{UDPConn: pc, ctrl: ctrl, header: socksUDPHeader(dst)}, nil
	}
}

// associate runs the SOCKS5 greeting and UDP ASSOCIATE on ctrl and returns the
// relay the datagrams go to. An unspecified relay address means the server's.
func associate(ctrl net.Conn) (netip.AddrPort, error) {
	_ = ctrl.SetDeadline(time.Now().Add(dialTimeout))
	defer func() { _ = ctrl.SetDeadline(time.Time{}) }()
	if _, err := ctrl.Write([]byte{socksVersion, 1, socksNoAuth}); err != nil {
		return netip.AddrPort{}, err
	}
	greeting := make([]byte, 2)
	if _, err := io.ReadFull(ctrl, greeting); err != nil {
		return netip.AddrPort{}, err
	}
	if greeting[0] != socksVersion || greeting[1] != socksNoAuth {
		return netip.AddrPort{}, fmt.Errorf("greeting %v", greeting)
	}
	if _, err := ctrl.Write([]byte{socksVersion, socksCmdUDPAssociate, 0, socksAtypIPv4, 0, 0, 0, 0, 0, 0}); err != nil {
		return netip.AddrPort{}, err
	}
	head := make([]byte, 4)
	if _, err := io.ReadFull(ctrl, head); err != nil {
		return netip.AddrPort{}, err
	}
	if head[1] != socksRepOK {
		return netip.AddrPort{}, fmt.Errorf("reply %d", head[1])
	}
	var addr netip.Addr
	switch head[3] {
	case socksAtypIPv4:
		raw := make([]byte, 4)
		if _, err := io.ReadFull(ctrl, raw); err != nil {
			return netip.AddrPort{}, err
		}
		addr = netip.AddrFrom4([4]byte(raw))
	case socksAtypIPv6:
		raw := make([]byte, 16)
		if _, err := io.ReadFull(ctrl, raw); err != nil {
			return netip.AddrPort{}, err
		}
		addr = netip.AddrFrom16([16]byte(raw))
	default:
		return netip.AddrPort{}, fmt.Errorf("relay address type %d", head[3])
	}
	portRaw := make([]byte, 2)
	if _, err := io.ReadFull(ctrl, portRaw); err != nil {
		return netip.AddrPort{}, err
	}
	if addr.IsUnspecified() {
		addr = netip.MustParseAddr("127.0.0.1")
	}
	return netip.AddrPortFrom(addr, binary.BigEndian.Uint16(portRaw)), nil
}

const socksCmdUDPAssociate = 3

// socksUDPHeader is the fixed prefix every datagram to the relay carries.
func socksUDPHeader(dst netip.AddrPort) []byte {
	head := []byte{0, 0, 0}
	if dst.Addr().Is4() {
		ip := dst.Addr().As4()
		head = append(head, socksAtypIPv4)
		head = append(head, ip[:]...)
	} else {
		ip := dst.Addr().As16()
		head = append(head, socksAtypIPv6)
		head = append(head, ip[:]...)
	}
	return binary.BigEndian.AppendUint16(head, dst.Port())
}

// socksUDPConn is a connected UDP socket to the relay that speaks the SOCKS
// datagram framing, so the udpgw side sees a plain conversation.
type socksUDPConn struct {
	*net.UDPConn
	ctrl   net.Conn
	header []byte
	buf    []byte
}

func (c *socksUDPConn) Write(p []byte) (int, error) {
	frame := make([]byte, 0, len(c.header)+len(p))
	frame = append(frame, c.header...)
	frame = append(frame, p...)
	if _, err := c.UDPConn.Write(frame); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *socksUDPConn) Read(p []byte) (int, error) {
	if c.buf == nil {
		c.buf = make([]byte, udpgwMaxFrame+64)
	}
	for {
		n, err := c.UDPConn.Read(c.buf)
		if err != nil {
			return 0, err
		}
		payload, ok := stripSOCKSUDPHeader(c.buf[:n])
		if !ok {
			continue
		}
		return copy(p, payload), nil
	}
}

func stripSOCKSUDPHeader(frame []byte) ([]byte, bool) {
	if len(frame) < 4 || frame[2] != 0 {
		return nil, false
	}
	var addrLen int
	switch frame[3] {
	case socksAtypIPv4:
		addrLen = 4
	case socksAtypIPv6:
		addrLen = 16
	case socksAtypDomain:
		if len(frame) < 5 {
			return nil, false
		}
		addrLen = 1 + int(frame[4])
	default:
		return nil, false
	}
	start := 4 + addrLen + 2
	if len(frame) < start {
		return nil, false
	}
	return frame[start:], true
}

func (c *socksUDPConn) Close() error {
	_ = c.ctrl.Close()
	return c.UDPConn.Close()
}
