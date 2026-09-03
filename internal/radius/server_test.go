package radius

import (
	"context"
	"net"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"layeh.com/radius"
	"layeh.com/radius/rfc2865"
	"layeh.com/radius/rfc2866"
	"layeh.com/radius/rfc2869"
)

// A sink that accepts one password and records what it was handed.
type recordingSink struct {
	mu       sync.Mutex
	password string
	logins   []string
	events   []Event
}

func (s *recordingSink) Authenticate(_ context.Context, username, password string, _ Peer) Verdict {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logins = append(s.logins, username+":"+password)
	if password != s.password {
		return Verdict{Reason: "wrong password"}
	}
	return Verdict{Accept: true, FramedIP: netip.MustParseAddr("10.9.0.7"), InterimSeconds: 30}
}

func (s *recordingSink) Account(_ context.Context, ev Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, ev)
}

// The server appends from its own goroutine; the test reads under the lock.
func (s *recordingSink) seenLogins() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.logins...)
}

func (s *recordingSink) seenEvents() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Event(nil), s.events...)
}

func freeUDP(t *testing.T) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := conn.LocalAddr().String()
	_ = conn.Close()
	return addr
}

// The Rejected hook is installed before Register opens the listener: the
// server reads it from its own goroutine, and a later write is a race.
func newTestServer(t *testing.T, rejected func(nasID, username string, peer Peer, reason string)) (*Server, *recordingSink) {
	t.Helper()
	srv, err := New("s3cret-host", freeUDP(t), freeUDP(t))
	if err != nil {
		t.Fatal(err)
	}
	srv.Rejected = rejected
	sink := &recordingSink{password: "open-sesame"}
	if err := srv.Register(NASIdentifier("openvpn", 4), sink); err != nil {
		t.Fatalf("register: %v", err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	return srv, sink
}

func accessRequest(secret, nasID, user, password string) *radius.Packet {
	p := radius.New(radius.CodeAccessRequest, []byte(secret))
	_ = rfc2865.UserName_SetString(p, user)
	_ = rfc2865.UserPassword_SetString(p, password)
	_ = rfc2865.NASIdentifier_SetString(p, nasID)
	_ = rfc2865.CallingStationID_SetString(p, "203.0.113.7")
	return p
}

func exchange(t *testing.T, addr string, p *radius.Packet) *radius.Packet {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	resp, err := radius.Exchange(ctx, p, addr)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	return resp
}

/*
The daemon cores that speak only RADIUS get one authenticator: PAP against the
sink for the NAS-Identifier the daemon was given. A right password is accepted
with the tunnel address the sink chose, a wrong one is rejected and reported,
and a daemon nobody registered is rejected however right its password.
*/
func TestAccessRequestIsAnsweredByTheNASsSink(t *testing.T) {
	var rejectMu sync.Mutex
	var rejected []string
	srv, sink := newTestServer(t, func(nasID, username string, peer Peer, reason string) {
		rejectMu.Lock()
		defer rejectMu.Unlock()
		rejected = append(rejected, nasID+"|"+username+"|"+peer.Source.String()+"|"+reason)
	})
	readRejected := func() []string {
		rejectMu.Lock()
		defer rejectMu.Unlock()
		return append([]string(nil), rejected...)
	}
	nas := NASIdentifier("openvpn", 4)

	resp := exchange(t, srv.AuthAddr(), accessRequest("s3cret-host", nas, "alice", "open-sesame"))
	if resp.Code != radius.CodeAccessAccept {
		t.Fatalf("right password: %v, want Access-Accept", resp.Code)
	}
	if ip := rfc2865.FramedIPAddress_Get(resp); ip.String() != "10.9.0.7" {
		t.Fatalf("Framed-IP-Address = %v, want the sink's 10.9.0.7", ip)
	}
	if got := rfc2869.AcctInterimInterval_Get(resp); got != 30 {
		t.Fatalf("Acct-Interim-Interval = %d, want 30: without it the NAS reports only at disconnect", got)
	}

	resp = exchange(t, srv.AuthAddr(), accessRequest("s3cret-host", nas, "alice", "nope"))
	if resp.Code != radius.CodeAccessReject || rfc2865.ReplyMessage_GetString(resp) != "wrong password" {
		t.Fatalf("wrong password: %v %q, want Access-Reject with the reason", resp.Code, rfc2865.ReplyMessage_GetString(resp))
	}
	if r := readRejected(); len(r) != 1 || r[0] != nas+"|alice|203.0.113.7|wrong password" {
		t.Fatalf("rejections reported: %v", r)
	}

	resp = exchange(t, srv.AuthAddr(), accessRequest("s3cret-host", NASIdentifier("openvpn", 99), "alice", "open-sesame"))
	if resp.Code != radius.CodeAccessReject {
		t.Fatalf("unknown NAS: %v, want Access-Reject", resp.Code)
	}
	if got := strings.Join(sink.seenLogins(), ","); got != "alice:open-sesame,alice:nope" {
		t.Fatalf("the sink saw %q; the unknown NAS must never reach it", got)
	}

	// A wrong secret is not a wrong password: the packet is discarded unanswered.
	ctx, cancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer cancel()
	if _, err := radius.Exchange(ctx, accessRequest("other-secret", nas, "alice", "open-sesame"), srv.AuthAddr()); err == nil {
		t.Fatal("a packet under the wrong secret was answered")
	}
}

// Accounting records reach the sink with the gigawords folded in, and the
// NAS gets its response whatever the sink does with them.
func TestAccountingIsFoldedAndAcknowledged(t *testing.T) {
	srv, sink := newTestServer(t, nil)
	p := radius.New(radius.CodeAccountingRequest, []byte("s3cret-host"))
	_ = rfc2865.UserName_SetString(p, "alice")
	_ = rfc2865.NASIdentifier_SetString(p, NASIdentifier("openvpn", 4))
	_ = rfc2865.FramedIPAddress_Set(p, net.ParseIP("10.9.0.7"))
	_ = rfc2865.CallingStationID_SetString(p, "203.0.113.7")
	_ = rfc2866.AcctStatusType_Set(p, rfc2866.AcctStatusType_Value_InterimUpdate)
	_ = rfc2866.AcctSessionID_SetString(p, "sess-1")
	_ = rfc2866.AcctInputOctets_Set(p, 5)
	_ = rfc2869.AcctInputGigawords_Set(p, 1)
	_ = rfc2866.AcctOutputOctets_Set(p, 4000)
	_ = rfc2866.AcctSessionTime_Set(p, 61)
	resp := exchange(t, srv.AcctAddr(), p)
	if resp.Code != radius.CodeAccountingResponse {
		t.Fatalf("accounting answered %v", resp.Code)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		n := len(sink.seenEvents())
		if n == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	events := sink.seenEvents()
	if len(events) != 1 {
		t.Fatalf("the sink got %d events", len(events))
	}
	ev := events[0]
	want := Event{
		Status: Interim, SessionID: "sess-1", Username: "alice", In: 1<<32 + 5, Out: 4000, SessionSeconds: 61,
		Peer: Peer{Source: netip.MustParseAddr("203.0.113.7"), FramedIP: netip.MustParseAddr("10.9.0.7")},
	}
	if ev != want {
		t.Fatalf("event = %+v\nwant  %+v", ev, want)
	}
}
