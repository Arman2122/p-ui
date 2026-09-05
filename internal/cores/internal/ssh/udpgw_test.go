package ssh

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/Arman2122/p-ui/internal/core"
)

// udpEcho answers every datagram with itself, from the port it was sent to.
func udpEcho(t *testing.T) netip.AddrPort {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	go func() {
		buf := make([]byte, 65535)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = pc.WriteTo(buf[:n], from)
		}
	}()
	return pc.LocalAddr().(*net.UDPAddr).AddrPort()
}

// udpgwFrame encodes one client message exactly as badvpn's client does.
func udpgwFrame(flags byte, conid uint16, dst netip.AddrPort, payload []byte) []byte {
	var body []byte
	body = append(body, flags)
	body = binary.LittleEndian.AppendUint16(body, conid)
	if dst.Addr().Is4() {
		ip := dst.Addr().As4()
		body = append(body, ip[:]...)
	} else {
		ip := dst.Addr().As16()
		body = append(body, ip[:]...)
	}
	body = binary.BigEndian.AppendUint16(body, dst.Port())
	body = append(body, payload...)
	frame := binary.LittleEndian.AppendUint16(nil, uint16(len(body)))
	return append(frame, body...)
}

func readUDPGWFrame(t *testing.T, r io.Reader) (flags byte, conid uint16, from netip.AddrPort, payload []byte) {
	t.Helper()
	head := make([]byte, 2)
	if _, err := io.ReadFull(r, head); err != nil {
		t.Fatalf("frame head: %v", err)
	}
	body := make([]byte, binary.LittleEndian.Uint16(head))
	if _, err := io.ReadFull(r, body); err != nil {
		t.Fatalf("frame body: %v", err)
	}
	flags, conid = body[0], binary.LittleEndian.Uint16(body[1:3])
	rest := body[3:]
	if flags&udpgwFlagIPv6 != 0 {
		from = netip.AddrPortFrom(netip.AddrFrom16([16]byte(rest[:16])), binary.BigEndian.Uint16(rest[16:18]))
		rest = rest[18:]
	} else {
		from = netip.AddrPortFrom(netip.AddrFrom4([4]byte(rest[:4])), binary.BigEndian.Uint16(rest[4:6]))
		rest = rest[6:]
	}
	return flags, conid, from, rest
}

func openUDPGW(t *testing.T, client *gossh.Client, port int) net.Conn {
	t.Helper()
	gw, err := client.Dial("tcp", net.JoinHostPort("127.0.0.1", itoa(port)))
	if err != nil {
		t.Fatalf("the UDPGW forward was refused: %v", err)
	}
	_ = gw.SetDeadline(time.Now().Add(10 * time.Second))
	return gw
}

/*
The product: an app opens one forward to the UDPGW address and every UDP
conversation rides it. A datagram reaches its destination and the reply comes
back under the same id carrying the address the app asked for; a keepalive is
swallowed; a new destination under an old id is a new conversation; and every
byte is billed to the client in the direction it travelled.
*/
func TestUDPGWCarriesDatagramsInsideTheTunnel(t *testing.T) {
	first, second := udpEcho(t), udpEcho(t)
	c, inst := startCore(t, `{"transport":"tcp","udpgw":true}`, userN(0))
	client, err := dialTCP(t, inst, "user0", "secret-0")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if _, err := c.CollectTraffic(context.Background()); err != nil {
		t.Fatal(err)
	}
	gw := openUDPGW(t, client, udpgwDefaultPort)
	defer gw.Close()

	if _, err := gw.Write(udpgwFrame(udpgwFlagKeepalive, 0, netip.AddrPort{}, nil)); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("udp"), 300)
	if _, err := gw.Write(udpgwFrame(0, 7, first, payload)); err != nil {
		t.Fatal(err)
	}
	flags, conid, from, got := readUDPGWFrame(t, gw)
	if flags != 0 || conid != 7 || from != first || !bytes.Equal(got, payload) {
		t.Fatalf("reply = flags %d conid %d from %s %q; want conid 7 from %s with the payload", flags, conid, from, got, first)
	}

	// The same id, a new destination: the conversation is rebuilt towards it.
	if _, err := gw.Write(udpgwFrame(0, 7, second, []byte("elsewhere"))); err != nil {
		t.Fatal(err)
	}
	_, conid, from, got = readUDPGWFrame(t, gw)
	if conid != 7 || from != second || string(got) != "elsewhere" {
		t.Fatalf("after a new destination the reply came from %s with %q", from, got)
	}

	deltas, err := c.CollectTraffic(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := int64(len(payload) + len("elsewhere"))
	if len(deltas) != 1 || deltas[0].Up != want || deltas[0].Down != want {
		t.Fatalf("billed %+v, want up=%d down=%d for the datagrams both ways", deltas, want, want)
	}
	sessions, _ := c.Sessions(context.Background())
	if len(sessions) != 1 || sessions[0].LastSeenUnixMilli == 0 {
		t.Fatalf("sessions = %+v", sessions)
	}
}

// With the gateway off, the port is an ordinary forward and nothing answers
// it; with it on, only the loopback target is the gateway.
func TestUDPGWAnswersOnlyItsOwnAddress(t *testing.T) {
	echo := udpEcho(t)
	_, inst := startCore(t, `{"transport":"tcp","udpgw":true,"udpgwPort":7400}`, userN(0))
	client, err := dialTCP(t, inst, "user0", "secret-0")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	gw := openUDPGW(t, client, 7400)
	defer gw.Close()
	if _, err := gw.Write(udpgwFrame(0, 1, echo, []byte("hi"))); err != nil {
		t.Fatal(err)
	}
	if _, _, _, got := readUDPGWFrame(t, gw); string(got) != "hi" {
		t.Fatalf("reply %q", got)
	}
	if fwd, err := client.Dial("tcp", "127.0.0.1:7300"); err == nil {
		_ = fwd.Close()
		t.Fatal("the default port answered while the inbound was told to use 7400")
	}
}

// Routed through Xray, UDP is refused rather than sent around the router.
func TestUDPGWIsRefusedWhileRoutedThroughXray(t *testing.T) {
	_, inst := startCore(t, `{"transport":"tcp","udpgw":true,"routeThroughXray":true,"routeXrayPort":1}`, userN(0))
	client, err := dialTCP(t, inst, "user0", "secret-0")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	gw, err := client.Dial("tcp", "127.0.0.1:7300")
	if err == nil {
		_ = gw.Close()
		t.Fatal("a UDPGW session was opened on an inbound that routes through Xray; its datagrams would leave around the router")
	}
	if !strings.Contains(err.Error(), "routes through Xray") {
		t.Fatalf("the refusal does not say why: %v", err)
	}
}

func TestUDPGWSettings(t *testing.T) {
	on, err := parseSettings(`{"transport":"tcp","udpgw":true}`)
	if err != nil || !on.UDPGW || on.UDPGWPort != udpgwDefaultPort {
		t.Fatalf("udpgw on without a port = %+v, %v; want the default port", on, err)
	}
	if _, err := parseSettings(`{"transport":"tcp","udpgw":true,"udpgwPort":70000}`); err == nil {
		t.Fatal("an impossible port was accepted")
	}
	off, err := parseSettings(`{"transport":"tcp"}`)
	if err != nil || off.UDPGW {
		t.Fatalf("udpgw is on by default: %+v", off)
	}
	inst := core.Instance{Listen: "127.0.0.1", Port: 22}
	if fingerprint(inst, on) == fingerprint(inst, off) {
		t.Fatal("turning the gateway on does not rebuild the listener")
	}
}
