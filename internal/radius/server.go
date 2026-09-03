/*
Package radius is the panel's embedded RADIUS server: the authenticator and
accounting source for the daemon cores that speak nothing else (ocserv,
OpenVPN's plugin, accel-ppp, strongSwan's eap-radius). One loopback listener
pair, one host secret, and a NAS-Identifier per inbound naming the sink that
answers for it; enforcement stays the panel's fan-out, never this server's.
*/
package radius

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"layeh.com/radius"
	"layeh.com/radius/rfc2865"
	"layeh.com/radius/rfc2866"
	"layeh.com/radius/rfc2869"

	"github.com/Arman2122/p-ui/internal/logger"
)

// warnWriter routes the library's own error lines into the panel log.
type warnWriter struct{}

func (warnWriter) Write(p []byte) (int, error) {
	logger.Warning("radius:", strings.TrimSpace(string(p)))
	return len(p), nil
}

const (
	DefaultAuthAddr = "127.0.0.1:1812"
	DefaultAcctAddr = "127.0.0.1:1813"
)

// Verdict is an authenticator's answer. Reason is logged and handed to the
// auth-failure event; ReplyMessage is what the client may be shown.
type Verdict struct {
	Accept       bool
	Reason       string
	ReplyMessage string
	// FramedIP, when set, is pushed to the NAS as the client's tunnel address.
	FramedIP netip.Addr
	// InterimSeconds asks the NAS for an accounting record this often; 0 leaves
	// only Start and Stop, and quota enforcement waits for the disconnect.
	InterimSeconds int
}

// Peer is where a login or a session came from, as the NAS reported it.
type Peer struct {
	// Source is Calling-Station-Id parsed as an address, or invalid.
	Source netip.Addr
	// FramedIP is the tunnel address the NAS assigned, or invalid.
	FramedIP netip.Addr
}

// Status is Acct-Status-Type as the panel reads it.
type Status int

const (
	Start Status = iota + 1
	Interim
	Stop
)

func (s Status) String() string {
	switch s {
	case Start:
		return "start"
	case Interim:
		return "interim"
	case Stop:
		return "stop"
	}
	return "unknown"
}

// Event is one accounting record with its octet counters folded to 64 bits.
type Event struct {
	Status    Status
	SessionID string
	Username  string
	Peer      Peer
	// In is what the client sent (Acct-Input), Out what it received, cumulative
	// for the session; gigawords are folded in.
	In, Out        int64
	SessionSeconds int64
	// TerminateCause is RFC 2866's number on a Stop, 0 otherwise.
	TerminateCause int
}

// Sink is what one inbound's core registers for its NAS-Identifier.
type Sink interface {
	Authenticate(ctx context.Context, username, password string, peer Peer) Verdict
	Account(ctx context.Context, ev Event)
}

// Server is the listener pair. Nothing listens until the first Register, so
// a panel with no RADIUS-speaking core opens no port.
type Server struct {
	authAddr, acctAddr string
	secret             []byte

	mu     sync.Mutex
	sinks  map[string]Sink
	auth   *radius.PacketServer
	acct   *radius.PacketServer
	closed bool
	// Rejected hears every refused login, for the panel's auth-failure event.
	Rejected func(nasID, username string, peer Peer, reason string)
}

// New builds a server for one host secret. Addresses default to loopback 1812/1813.
func New(secret string, authAddr, acctAddr string) (*Server, error) {
	if strings.TrimSpace(secret) == "" {
		return nil, errors.New("radius: an empty shared secret authenticates nobody")
	}
	if authAddr == "" {
		authAddr = DefaultAuthAddr
	}
	if acctAddr == "" {
		acctAddr = DefaultAcctAddr
	}
	return &Server{authAddr: authAddr, acctAddr: acctAddr, secret: []byte(secret), sinks: map[string]Sink{}}, nil
}

// AuthAddr and AcctAddr are what a core writes into its daemon's config.
func (s *Server) AuthAddr() string { return s.authAddr }
func (s *Server) AcctAddr() string { return s.acctAddr }

// Secret is the host secret a core writes into its daemon's config.
func (s *Server) Secret() string { return string(s.secret) }

// NASIdentifier is the name a core gives its inbound's daemon, so the packets
// come back to the right sink.
func NASIdentifier(coreID string, inboundID int) string {
	return fmt.Sprintf("p-ui-%s-%d", coreID, inboundID)
}

// Register binds a sink to a NAS-Identifier and starts listening if this is
// the first; re-registering replaces the sink.
func (s *Server) Register(nasID string, sink Sink) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("radius: server closed")
	}
	s.sinks[nasID] = sink
	if s.auth != nil {
		return nil
	}
	return s.listenLocked()
}

// Unregister forgets a sink; packets naming it are rejected from then on.
func (s *Server) Unregister(nasID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sinks, nasID)
}

func (s *Server) listenLocked() error {
	lc := &net.ListenConfig{}
	authConn, err := lc.ListenPacket(context.Background(), "udp", s.authAddr)
	if err != nil {
		return fmt.Errorf("radius: listen %s: %w", s.authAddr, err)
	}
	acctConn, err := lc.ListenPacket(context.Background(), "udp", s.acctAddr)
	if err != nil {
		_ = authConn.Close()
		return fmt.Errorf("radius: listen %s: %w", s.acctAddr, err)
	}
	s.auth = &radius.PacketServer{SecretSource: radius.StaticSecretSource(s.secret), Handler: radius.HandlerFunc(s.handleAuth), ErrorLog: log.New(warnWriter{}, "", 0)}
	s.acct = &radius.PacketServer{SecretSource: radius.StaticSecretSource(s.secret), Handler: radius.HandlerFunc(s.handleAcct), ErrorLog: log.New(warnWriter{}, "", 0)}
	go func() {
		if err := s.auth.Serve(authConn); err != nil && !errors.Is(err, radius.ErrServerShutdown) {
			logger.Warning("radius: auth listener:", err)
		}
	}()
	go func() {
		if err := s.acct.Serve(acctConn); err != nil && !errors.Is(err, radius.ErrServerShutdown) {
			logger.Warning("radius: accounting listener:", err)
		}
	}()
	logger.Infof("radius: listening on %s (auth) and %s (accounting)", s.authAddr, s.acctAddr)
	return nil
}

// Close stops both listeners.
func (s *Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var errs []error
	for _, srv := range []*radius.PacketServer{s.auth, s.acct} {
		if srv != nil {
			errs = append(errs, srv.Shutdown(ctx))
		}
	}
	s.auth, s.acct = nil, nil
	return errors.Join(errs...)
}

func (s *Server) sinkFor(p *radius.Packet) (string, Sink, bool) {
	nasID := rfc2865.NASIdentifier_GetString(p)
	s.mu.Lock()
	defer s.mu.Unlock()
	sink, ok := s.sinks[nasID]
	return nasID, sink, ok
}

func peerOf(p *radius.Packet) Peer {
	peer := Peer{}
	if src, err := netip.ParseAddr(strings.TrimSpace(rfc2865.CallingStationID_GetString(p))); err == nil {
		peer.Source = src
	}
	if ip := rfc2865.FramedIPAddress_Get(p); ip != nil {
		if addr, ok := netip.AddrFromSlice(ip); ok {
			peer.FramedIP = addr.Unmap()
		}
	}
	return peer
}

// handleAuth answers Access-Request with PAP only: every daemon this serves
// speaks it, and the panel holds the password to compare against.
func (s *Server) handleAuth(w radius.ResponseWriter, r *radius.Request) {
	if r.Code != radius.CodeAccessRequest {
		return
	}
	username := rfc2865.UserName_GetString(r.Packet)
	peer := peerOf(r.Packet)
	nasID, sink, ok := s.sinkFor(r.Packet)
	if !ok {
		logger.Warningf("radius: Access-Request for unknown NAS %q (user %q) rejected", nasID, username)
		_ = w.Write(r.Response(radius.CodeAccessReject))
		return
	}
	password := rfc2865.UserPassword_GetString(r.Packet)
	if password == "" {
		s.reject(w, r, nasID, username, peer, "no PAP password: only PAP is served")
		return
	}
	verdict := sink.Authenticate(r.Context(), username, password, peer)
	if !verdict.Accept {
		s.reject(w, r, nasID, username, peer, verdict.Reason)
		return
	}
	resp := r.Response(radius.CodeAccessAccept)
	if verdict.FramedIP.IsValid() && verdict.FramedIP.Is4() {
		_ = rfc2865.FramedIPAddress_Set(resp, net.IP(verdict.FramedIP.AsSlice()))
	}
	if verdict.ReplyMessage != "" {
		_ = rfc2865.ReplyMessage_SetString(resp, verdict.ReplyMessage)
	}
	if verdict.InterimSeconds > 0 {
		_ = rfc2869.AcctInterimInterval_Set(resp, rfc2869.AcctInterimInterval(verdict.InterimSeconds))
	}
	_ = w.Write(resp)
}

func (s *Server) reject(w radius.ResponseWriter, r *radius.Request, nasID, username string, peer Peer, reason string) {
	if s.Rejected != nil {
		s.Rejected(nasID, username, peer, reason)
	}
	resp := r.Response(radius.CodeAccessReject)
	if reason != "" {
		_ = rfc2865.ReplyMessage_SetString(resp, reason)
	}
	_ = w.Write(resp)
}

// handleAcct folds the gigawords and hands the record to the sink. The
// response is sent first: a NAS retries an unanswered record, and a sink that
// is slow must never make it double-count.
func (s *Server) handleAcct(w radius.ResponseWriter, r *radius.Request) {
	if r.Code != radius.CodeAccountingRequest {
		return
	}
	nasID, sink, ok := s.sinkFor(r.Packet)
	if !ok {
		logger.Warningf("radius: accounting for unknown NAS %q dropped", nasID)
		return
	}
	_ = w.Write(r.Response(radius.CodeAccountingResponse))
	var status Status
	switch rfc2866.AcctStatusType_Get(r.Packet) {
	case rfc2866.AcctStatusType_Value_Start:
		status = Start
	case rfc2866.AcctStatusType_Value_InterimUpdate:
		status = Interim
	case rfc2866.AcctStatusType_Value_Stop:
		status = Stop
	default:
		return
	}
	ev := Event{
		Status:         status,
		SessionID:      rfc2866.AcctSessionID_GetString(r.Packet),
		Username:       rfc2865.UserName_GetString(r.Packet),
		Peer:           peerOf(r.Packet),
		In:             int64(rfc2869.AcctInputGigawords_Get(r.Packet))<<32 | int64(rfc2866.AcctInputOctets_Get(r.Packet)),
		Out:            int64(rfc2869.AcctOutputGigawords_Get(r.Packet))<<32 | int64(rfc2866.AcctOutputOctets_Get(r.Packet)),
		SessionSeconds: int64(rfc2866.AcctSessionTime_Get(r.Packet)),
		TerminateCause: int(rfc2866.AcctTerminateCause_Get(r.Packet)),
	}
	sink.Account(r.Context(), ev)
}
