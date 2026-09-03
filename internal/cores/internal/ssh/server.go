package ssh

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	gossh "golang.org/x/crypto/ssh"
	"golang.org/x/time/rate"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/logger"
)

// AuthFailure is one refused login, handed to the panel so it can alert and
// ban without the core knowing how.
type AuthFailure struct {
	InstanceID int
	Username   string
	IP         string
	Reason     string
}

const (
	maxAuthTries   = 3
	dialTimeout    = 10 * time.Second
	minBurstBytes  = 16 * 1024
	serverVersion  = "SSH-2.0-OpenSSH_9.6"
	dirUp          = "up"
	dirDown        = "down"
	sessionChannel = "session"
	forwardChannel = "direct-tcpip"
)

// Dialer opens the forward's destination; the default dials directly, and a
// SOCKS bridge into Xray is one swap of this function away.
type Dialer func(ctx context.Context, network, address string) (net.Conn, error)

// server is one inbound's listener and everything that hangs off it.
type server struct {
	instanceID int
	tag        string
	settings   Settings
	hostKey    gossh.Signer
	dial       Dialer
	onFailure  func(AuthFailure)

	mu       sync.RWMutex
	byUser   map[string]*credential
	byEmail  map[string]*credential
	conns    map[*conn]struct{}
	traffic  map[string]*counters
	limits   map[string]core.RateLimit
	limiters map[string]*limiterPair

	ln     net.Listener
	closed atomic.Bool
	wg     sync.WaitGroup
}

type counters struct{ up, down atomic.Int64 }

type limiterPair struct{ up, down *rate.Limiter }

// conn is one authenticated SSH connection.
type conn struct {
	sc       *gossh.ServerConn
	email    string
	source   netip.Addr
	lastSeen atomic.Int64
}

// close tolerates a session the tests put there without a transport behind it.
func (c *conn) close() {
	if c.sc != nil {
		_ = c.sc.Close()
	}
}

func newServer(instanceID int, tag string, settings Settings, hostKey gossh.Signer, dial Dialer, onFailure func(AuthFailure)) *server {
	if dial == nil {
		dial = (&net.Dialer{Timeout: dialTimeout}).DialContext
	}
	return &server{
		instanceID: instanceID, tag: tag, settings: settings, hostKey: hostKey, dial: dial, onFailure: onFailure,
		byUser: map[string]*credential{}, byEmail: map[string]*credential{},
		conns: map[*conn]struct{}{}, traffic: map[string]*counters{},
		limits: map[string]core.RateLimit{}, limiters: map[string]*limiterPair{},
	}
}

// setUsers replaces the credential set. A client no longer present, or now
// disabled, has every live connection closed: that is what a revocation means.
func (s *server) setUsers(users []core.User) error {
	next := map[string]*credential{}
	nextEmail := map[string]*credential{}
	var failures []error
	for _, u := range users {
		c, err := credentialOf(u)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		cred := c
		next[cred.username] = &cred
		nextEmail[cred.email] = &cred
	}
	s.mu.Lock()
	s.byUser, s.byEmail = next, nextEmail
	var doomed []*conn
	for c := range s.conns {
		cred, ok := nextEmail[c.email]
		if !ok || !cred.enable {
			doomed = append(doomed, c)
		}
	}
	s.mu.Unlock()
	for _, c := range doomed {
		c.close()
	}
	return errors.Join(failures...)
}

func (s *server) servedEmails() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.byEmail))
	for email := range s.byEmail {
		out = append(out, email)
	}
	return out
}

func (s *server) config() *gossh.ServerConfig {
	cfg := &gossh.ServerConfig{
		MaxAuthTries:     maxAuthTries,
		ServerVersion:    serverVersion,
		PasswordCallback: s.authenticate,
		AuthLogCallback: func(meta gossh.ConnMetadata, method string, err error) {
			if err == nil || method == "none" || s.onFailure == nil {
				return
			}
			s.onFailure(AuthFailure{InstanceID: s.instanceID, Username: meta.User(), IP: hostOf(meta.RemoteAddr()), Reason: err.Error()})
		},
	}
	cfg.AddHostKey(s.hostKey)
	return cfg
}

var errDenied = errors.New("permission denied")

// authenticate is the one gate: the password, the client's own enable and
// expiry, and its device cap counted as distinct source addresses.
func (s *server) authenticate(meta gossh.ConnMetadata, password []byte) (*gossh.Permissions, error) {
	s.mu.RLock()
	cred, ok := s.byUser[meta.User()]
	s.mu.RUnlock()
	if !ok || subtle.ConstantTimeCompare([]byte(cred.password), password) != 1 {
		return nil, errDenied
	}
	if !cred.enable {
		return nil, errors.New("client is disabled")
	}
	if cred.expiry > 0 && time.Now().UnixMilli() > cred.expiry {
		return nil, errors.New("client has expired")
	}
	source := addrOf(meta.RemoteAddr())
	if cred.limitIP > 0 && s.distinctSources(cred.email, source) >= cred.limitIP {
		return nil, fmt.Errorf("device limit of %d reached", cred.limitIP)
	}
	return &gossh.Permissions{Extensions: map[string]string{"email": cred.email}}, nil
}

// distinctSources counts the addresses one client is connected from, not
// counting the one asking, so a reconnect from a known device is never refused.
func (s *server) distinctSources(email string, except netip.Addr) int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := map[netip.Addr]struct{}{}
	for c := range s.conns {
		if c.email == email && c.source != except {
			seen[c.source] = struct{}{}
		}
	}
	return len(seen)
}

func (s *server) serve(ln net.Listener) {
	s.ln = ln
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			raw, err := ln.Accept()
			if err != nil {
				if s.closed.Load() {
					return
				}
				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() {
					continue
				}
				logger.Warning("ssh: accept on", ln.Addr(), "failed:", err)
				return
			}
			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				s.handle(raw)
			}()
		}
	}()
}

// handle runs one transport connection through the SSH handshake and its
// channels; the transports hand it a net.Conn and never see SSH.
func (s *server) handle(raw net.Conn) {
	sc, chans, reqs, err := gossh.NewServerConn(raw, s.config())
	if err != nil {
		_ = raw.Close()
		return
	}
	c := &conn{sc: sc, email: sc.Permissions.Extensions["email"], source: addrOf(sc.RemoteAddr())}
	c.lastSeen.Store(time.Now().UnixMilli())
	s.mu.Lock()
	s.conns[c] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
		_ = sc.Close()
	}()
	go func() {
		// Keepalives are answered; a reverse forward (tcpip-forward) is not a
		// service this core offers, so every other request is refused.
		for req := range reqs {
			if req.WantReply {
				_ = req.Reply(req.Type == "keepalive@openssh.com", nil)
			}
		}
	}()
	for nc := range chans {
		switch nc.ChannelType() {
		case forwardChannel:
			go s.forward(c, nc)
		case sessionChannel:
			go s.session(nc)
		default:
			_ = nc.Reject(gossh.UnknownChannelType, "unsupported channel")
		}
	}
}

// session is the shell channel many clients open as a liveness probe: the
// requests are answered, the banner written, and nothing is ever executed.
func (s *server) session(nc gossh.NewChannel) {
	ch, reqs, err := nc.Accept()
	if err != nil {
		return
	}
	defer ch.Close()
	go func() {
		for req := range reqs {
			switch req.Type {
			case "pty-req", "env", "window-change":
				if req.WantReply {
					_ = req.Reply(true, nil)
				}
			case "shell":
				if req.WantReply {
					_ = req.Reply(true, nil)
				}
				if s.settings.Banner != "" {
					_, _ = io.WriteString(ch, s.settings.Banner+"\r\n")
				}
			default:
				if req.WantReply {
					_ = req.Reply(false, nil)
				}
			}
		}
	}()
	_, _ = io.Copy(io.Discard, ch)
}

// forward is the product: one direct-tcpip channel spliced to its destination,
// every byte counted to the client and throttled to what the client may push.
func (s *server) forward(c *conn, nc gossh.NewChannel) {
	var req struct {
		Host       string
		Port       uint32
		OriginAddr string
		OriginPort uint32
	}
	if err := gossh.Unmarshal(nc.ExtraData(), &req); err != nil {
		_ = nc.Reject(gossh.ConnectionFailed, "bad direct-tcpip payload")
		return
	}
	if !s.stillServed(c.email) {
		_ = nc.Reject(gossh.Prohibited, "client is no longer served")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), dialTimeout)
	dst, err := s.dial(ctx, "tcp", net.JoinHostPort(req.Host, strconv.Itoa(int(req.Port))))
	cancel()
	if err != nil {
		_ = nc.Reject(gossh.ConnectionFailed, err.Error())
		return
	}
	ch, reqs, err := nc.Accept()
	if err != nil {
		_ = dst.Close()
		return
	}
	go gossh.DiscardRequests(reqs)
	up, down := s.countersFor(c.email)
	lim := s.limiterFor(c.email)
	var once sync.Once
	closeBoth := func() { once.Do(func() { _ = ch.Close(); _ = dst.Close() }) }
	go func() {
		defer closeBoth()
		_, _ = io.Copy(&countingWriter{w: dst, n: up, lim: lim.up, seen: &c.lastSeen}, ch)
	}()
	defer closeBoth()
	_, _ = io.Copy(&countingWriter{w: ch, n: down, lim: lim.down, seen: &c.lastSeen}, dst)
}

func (s *server) stillServed(email string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cred, ok := s.byEmail[email]
	return ok && cred.enable
}

func (s *server) countersFor(email string) (*atomic.Int64, *atomic.Int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ct, ok := s.traffic[email]
	if !ok {
		ct = &counters{}
		s.traffic[email] = ct
	}
	return &ct.up, &ct.down
}

// limiterFor hands out the client's limiters; they are shared by every one of
// the client's connections so the cap is per client, not per connection.
func (s *server) limiterFor(email string) *limiterPair {
	s.mu.Lock()
	defer s.mu.Unlock()
	pair, ok := s.limiters[email]
	if !ok {
		pair = &limiterPair{up: rate.NewLimiter(rate.Inf, 0), down: rate.NewLimiter(rate.Inf, 0)}
		s.limiters[email] = pair
	}
	applyLimit(pair, s.limits[email])
	return pair
}

// setLimits applies the panel's evaluated ladder. Every live connection picks
// the new rate up on its next write; an email left out is unlimited.
func (s *server) setLimits(limits map[string]core.RateLimit) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limits = limits
	for email, pair := range s.limiters {
		applyLimit(pair, limits[email])
	}
}

func applyLimit(pair *limiterPair, l core.RateLimit) {
	for _, d := range []struct {
		lim *rate.Limiter
		bps int64
	}{{pair.up, l.UpBps}, {pair.down, l.DownBps}} {
		if d.bps <= 0 {
			d.lim.SetLimit(rate.Inf)
			d.lim.SetBurst(0)
			continue
		}
		perSec := d.bps / 8
		if perSec < 1 {
			perSec = 1
		}
		d.lim.SetLimit(rate.Limit(perSec))
		d.lim.SetBurst(int(max(perSec, minBurstBytes)))
	}
}

// countingWriter bills each byte it moves, refreshes the session's activity
// and, when a rate is set, waits for the tokens before it moves them.
type countingWriter struct {
	w    io.Writer
	n    *atomic.Int64
	lim  *rate.Limiter
	seen *atomic.Int64
}

func (cw *countingWriter) Write(p []byte) (int, error) {
	written := 0
	for len(p) > 0 {
		chunk := p
		if cw.lim.Limit() != rate.Inf {
			if burst := cw.lim.Burst(); burst > 0 && len(chunk) > burst {
				chunk = p[:burst]
			}
			if err := cw.lim.WaitN(context.Background(), len(chunk)); err != nil {
				return written, err
			}
		}
		// Billed before the write lands: the far side can echo the bytes back and
		// a scrape read them before a post-write add would have run.
		cw.n.Add(int64(len(chunk)))
		n, err := cw.w.Write(chunk)
		written += n
		if n < len(chunk) {
			cw.n.Add(int64(n - len(chunk)))
		}
		cw.seen.Store(time.Now().UnixMilli())
		if err != nil {
			return written, err
		}
		p = p[n:]
	}
	return written, nil
}

// readings is the cumulative view the Counter engine converts into deltas.
func (s *server) readings() map[string]int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]int64, 2*len(s.traffic))
	for email, ct := range s.traffic {
		out[email+"|"+dirUp] = ct.up.Load()
		out[email+"|"+dirDown] = ct.down.Load()
	}
	return out
}

func (s *server) resetTraffic() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ct := range s.traffic {
		ct.up.Store(0)
		ct.down.Store(0)
	}
}

func (s *server) sessions() []core.Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]core.Session, 0, len(s.conns))
	for c := range s.conns {
		out = append(out, core.Session{Email: c.email, Source: c.source, LastSeenUnixMilli: c.lastSeen.Load()})
	}
	return out
}

func (s *server) online() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := map[string]struct{}{}
	for c := range s.conns {
		seen[c.email] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for email := range seen {
		out = append(out, email)
	}
	return out
}

func (s *server) stop() {
	if !s.closed.CompareAndSwap(false, true) {
		return
	}
	if s.ln != nil {
		_ = s.ln.Close()
	}
	s.mu.RLock()
	conns := make([]*conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.RUnlock()
	for _, c := range conns {
		c.close()
	}
	s.wg.Wait()
}

func addrOf(a net.Addr) netip.Addr {
	if a == nil {
		return netip.Addr{}
	}
	if ap, err := netip.ParseAddrPort(a.String()); err == nil {
		return ap.Addr().Unmap()
	}
	if ip, err := netip.ParseAddr(hostOf(a)); err == nil {
		return ip.Unmap()
	}
	return netip.Addr{}
}

func hostOf(a net.Addr) string {
	if a == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(a.String())
	if err != nil {
		return a.String()
	}
	return host
}
