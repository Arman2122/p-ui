package ssh

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"testing"
	"time"
)

// dnsRelay stands in for the Xray bridge: a SOCKS5 server that records each
// CONNECT target and answers every length-framed query with one framed answer.
func dnsRelay(t *testing.T, answer []byte) (port int, targets <-chan string, queries <-chan []byte) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	seen, asked := make(chan string, 8), make(chan []byte, 8)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				buf := make([]byte, 260)
				if _, err := io.ReadFull(c, buf[:2]); err != nil {
					return
				}
				if _, err := io.ReadFull(c, buf[:int(buf[1])]); err != nil {
					return
				}
				_, _ = c.Write([]byte{5, 0})
				if _, err := io.ReadFull(c, buf[:4]); err != nil {
					return
				}
				var host string
				switch buf[3] {
				case 1:
					_, _ = io.ReadFull(c, buf[:4])
					host = netip.AddrFrom4([4]byte(buf[:4])).String()
				case 3:
					_, _ = io.ReadFull(c, buf[:1])
					n := int(buf[0])
					_, _ = io.ReadFull(c, buf[:n])
					host = string(buf[:n])
				case 4:
					_, _ = io.ReadFull(c, buf[:16])
					host = netip.AddrFrom16([16]byte(buf[:16])).String()
				}
				_, _ = io.ReadFull(c, buf[:2])
				seen <- net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(buf[:2]))))
				_, _ = c.Write([]byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
				for {
					if _, err := io.ReadFull(c, buf[:2]); err != nil {
						return
					}
					q := make([]byte, binary.BigEndian.Uint16(buf[:2]))
					if _, err := io.ReadFull(c, q); err != nil {
						return
					}
					asked <- q
					frame := binary.BigEndian.AppendUint16(nil, uint16(len(answer)))
					_, _ = c.Write(append(frame, answer...))
				}
			}()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, seen, asked
}

func recvWithin[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatalf("no %s within 5s", what)
		panic("unreachable")
	}
}

/*
On a routed inbound a DNS-flagged datagram went to the host's own resolver,
straight from the server: the one lookup of every session left around the
route with the server's address, and a routed-through-an-exit setup resolved
from the wrong country. It now travels through the bridge as DNS over TCP,
which every exit carries, to the resolver the app named when an exit can reach
it and to a public one when the app named the phone's own.
*/
func TestRoutedUDPGWResolvesThroughTheRoute(t *testing.T) {
	port, targets, queries := dnsRelay(t, []byte("answer"))
	_, inst := startCore(t, fmt.Sprintf(`{"transport":"tcp","udpgw":true,"routeThroughXray":true,"routeXrayPort":%d}`, port), userN(0))
	client, err := dialTCP(t, inst, "user0", "secret-0")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	gw := openUDPGW(t, client, udpgwDefaultPort)
	defer gw.Close()

	phone := netip.MustParseAddrPort("192.168.43.1:53")
	if _, err := gw.Write(udpgwFrame(udpgwFlagDNS, 1, phone, []byte("query"))); err != nil {
		t.Fatal(err)
	}
	if target := recvWithin(t, targets, "bridge CONNECT"); target != routedResolver.String() {
		t.Fatalf("the phone's resolver was replaced by %s, want %s", target, routedResolver)
	}
	if q := recvWithin(t, queries, "framed query"); string(q) != "query" {
		t.Fatalf("the resolver received %q, want the datagram framed as one TCP message", q)
	}
	_, conid, from, payload := readUDPGWFrame(t, gw)
	if conid != 1 || from != phone || string(payload) != "answer" {
		t.Fatalf("reply = id %d from %s %q; want the answer under the app's own id and address", conid, from, payload)
	}

	public := netip.MustParseAddrPort("9.9.9.9:53")
	if _, err := gw.Write(udpgwFrame(udpgwFlagDNS, 2, public, []byte("query2"))); err != nil {
		t.Fatal(err)
	}
	if target := recvWithin(t, targets, "second CONNECT"); target != public.String() {
		t.Fatalf("a public resolver the app named was replaced by %s", target)
	}
	if _, conid, _, payload := readUDPGWFrame(t, gw); conid != 2 || string(payload) != "answer" {
		t.Fatalf("second reply = id %d %q", conid, payload)
	}
}
