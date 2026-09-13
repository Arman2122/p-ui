package ssh

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/logger"
)

/*
The exit half: the panel dials a remote SSH server and exposes a loopback
SOCKS5 port that routing rules send traffic to. The compiler already emits a
socks outbound for an ExitSocksPort handle and narrows the rule to TCP.
*/

// ExitSettings is the egress row's settings JSON for an ssh exit.
type ExitSettings struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	// PrivateKey is a PEM private key, the alternative to a password.
	PrivateKey string `json:"privateKey"`
	// HostKeyFingerprint pins the server: "SHA256:..." as ssh-keygen -lf prints it.
	HostKeyFingerprint string `json:"hostKeyFingerprint"`
}

func parseExitSettings(raw string) (ExitSettings, error) {
	var s ExitSettings
	if strings.TrimSpace(raw) == "" {
		return s, errors.New("ssh exit: no settings")
	}
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return s, fmt.Errorf("ssh exit: settings: %w", err)
	}
	s.Host, s.Username = strings.TrimSpace(s.Host), strings.TrimSpace(s.Username)
	s.HostKeyFingerprint = strings.TrimSpace(s.HostKeyFingerprint)
	if s.Port == 0 {
		s.Port = 22
	}
	switch {
	case s.Host == "":
		return s, errors.New("ssh exit: a host is required")
	case s.Port < 1 || s.Port > 65535:
		return s, fmt.Errorf("ssh exit: port %d is out of range", s.Port)
	case s.Username == "":
		return s, errors.New("ssh exit: a username is required")
	case s.Password == "" && s.PrivateKey == "":
		return s, errors.New("ssh exit: a password or a private key is required")
	case !strings.HasPrefix(s.HostKeyFingerprint, "SHA256:"):
		return s, errors.New("ssh exit: the server's host key fingerprint is required (ssh-keyscan -t ed25519 HOST | ssh-keygen -lf -)")
	}
	if s.PrivateKey != "" {
		if _, err := gossh.ParsePrivateKey([]byte(s.PrivateKey)); err != nil {
			return s, fmt.Errorf("ssh exit: private key: %w", err)
		}
	}
	return s, nil
}

func (s ExitSettings) hash() string {
	b, _ := json.Marshal(s)
	return string(b)
}

func (c *Core) ExitKinds() []core.Kind { return []core.Kind{Kind} }

func (c *Core) ExitHandleKind(kind core.Kind) core.ExitHandleKind {
	if kind != Kind {
		return core.ExitNone
	}
	return core.ExitSocksPort
}

// ExitHandle is pure: the port is a function of the row id, so a compile or a
// preview never starts anything. ReconcileExits is what connects.
func (c *Core) ExitHandle(_ context.Context, exit core.Exit) (core.ExitHandle, error) {
	if exit.Kind != Kind {
		return core.ExitHandle{}, nil
	}
	return core.ExitHandle{SocksPort: core.ExitSocksPortFor(exit.ID), Source: core.SourceOwnerDaemon, TCPOnly: true}, nil
}

func (c *Core) ValidateExit(exit core.Exit) error {
	if exit.Kind != Kind {
		return nil
	}
	_, err := parseExitSettings(exit.Settings)
	return err
}

// ReconcileExits converges the exit clients on desired: a changed row is
// redialled, an unchanged one kept with its sessions, a missing one closed.
func (c *Core) ReconcileExits(_ context.Context, desired []core.Exit) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	want := map[int]struct{}{}
	var failures []error
	for _, exit := range desired {
		if exit.Kind != Kind {
			continue
		}
		if !exit.Enable {
			continue
		}
		want[exit.ID] = struct{}{}
		settings, err := parseExitSettings(exit.Settings)
		if err != nil {
			failures = append(failures, fmt.Errorf("exit %d: %w", exit.ID, err))
			continue
		}
		if cur, ok := c.exits[exit.ID]; ok {
			if cur.settings.hash() == settings.hash() {
				continue
			}
			cur.stop()
			delete(c.exits, exit.ID)
		}
		ex, err := startExit(exit.ID, settings, c.deps.Dial)
		if err != nil {
			failures = append(failures, fmt.Errorf("exit %d: %w", exit.ID, err))
			continue
		}
		c.exits[exit.ID] = ex
	}
	for id, cur := range c.exits {
		if _, ok := want[id]; !ok {
			cur.stop()
			delete(c.exits, id)
		}
	}
	return errors.Join(failures...)
}

// exitClient is one row's SSH connection and the SOCKS port in front of it.
type exitClient struct {
	id       int
	settings ExitSettings
	dial     Dialer

	ln net.Listener

	mu     sync.Mutex
	client *gossh.Client
	closed bool
	wg     sync.WaitGroup
}

const (
	exitKeepalive  = 30 * time.Second
	exitBackoffMax = 30 * time.Second
)

// exitDialTimeout bounds the dial and the handshake; a test shortens it.
var exitDialTimeout = 10 * time.Second

func startExit(id int, settings ExitSettings, dial Dialer) (*exitClient, error) {
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(core.ExitSocksPortFor(id))))
	if err != nil {
		return nil, fmt.Errorf("socks port: %w", err)
	}
	if dial == nil {
		dial = (&net.Dialer{Timeout: exitDialTimeout}).DialContext
	}
	ex := &exitClient{id: id, settings: settings, dial: dial, ln: ln}
	ex.wg.Add(2)
	go ex.maintain()
	go ex.serveSocks()
	logger.Infof("ssh: exit %d serving SOCKS on %s towards %s@%s:%d", id, ln.Addr(), settings.Username, settings.Host, settings.Port)
	return ex, nil
}

func (ex *exitClient) stop() {
	ex.mu.Lock()
	ex.closed = true
	client := ex.client
	ex.client = nil
	ex.mu.Unlock()
	_ = ex.ln.Close()
	if client != nil {
		_ = client.Close()
	}
	ex.wg.Wait()
}

func (ex *exitClient) isClosed() bool {
	ex.mu.Lock()
	defer ex.mu.Unlock()
	return ex.closed
}

func (ex *exitClient) clientConfig() (*gossh.ClientConfig, error) {
	var auth []gossh.AuthMethod
	if ex.settings.PrivateKey != "" {
		signer, err := gossh.ParsePrivateKey([]byte(ex.settings.PrivateKey))
		if err != nil {
			return nil, err
		}
		auth = append(auth, gossh.PublicKeys(signer))
	}
	if ex.settings.Password != "" {
		auth = append(auth, gossh.Password(ex.settings.Password))
	}
	want := ex.settings.HostKeyFingerprint
	return &gossh.ClientConfig{
		User:              ex.settings.Username,
		Auth:              auth,
		HostKeyAlgorithms: PreferredHostKeyAlgorithms,
		HostKeyCallback: func(_ string, _ net.Addr, key gossh.PublicKey) error {
			if got := gossh.FingerprintSHA256(key); got != want {
				return fmt.Errorf("host key %s does not match the pinned %s", got, want)
			}
			return nil
		},
		Timeout: exitDialTimeout,
	}, nil
}

// maintain keeps one SSH connection up: dial, keep it alive, redial with a
// backoff when it drops. The SOCKS listener stays up throughout and refuses
// while there is nothing to forward through.
func (ex *exitClient) maintain() {
	defer ex.wg.Done()
	backoff := time.Second
	for !ex.isClosed() {
		client, err := ex.connect()
		if err != nil {
			logger.Warningf("ssh: exit %d: %v; retrying in %s", ex.id, err, backoff)
			if !ex.sleep(backoff) {
				return
			}
			backoff = min(backoff*2, exitBackoffMax)
			continue
		}
		backoff = time.Second
		ex.mu.Lock()
		if ex.closed {
			ex.mu.Unlock()
			_ = client.Close()
			return
		}
		ex.client = client
		ex.mu.Unlock()
		ex.keepAlive(client)
		ex.mu.Lock()
		if ex.client == client {
			ex.client = nil
		}
		ex.mu.Unlock()
		_ = client.Close()
	}
}

func (ex *exitClient) connect() (*gossh.Client, error) {
	cfg, err := ex.clientConfig()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), exitDialTimeout)
	defer cancel()
	addr := net.JoinHostPort(ex.settings.Host, strconv.Itoa(ex.settings.Port))
	raw, err := ex.dial(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	// ClientConfig.Timeout covers the connect alone; a peer that accepts and
	// never speaks would otherwise hold maintain() for good.
	_ = raw.SetDeadline(time.Now().Add(exitDialTimeout))
	sc, chans, reqs, err := gossh.NewClientConn(raw, addr, cfg)
	if err != nil {
		_ = raw.Close()
		return nil, err
	}
	_ = raw.SetDeadline(time.Time{})
	return gossh.NewClient(sc, chans, reqs), nil
}

// keepAlive returns when the connection is gone.
func (ex *exitClient) keepAlive(client *gossh.Client) {
	ticker := time.NewTicker(exitKeepalive)
	defer ticker.Stop()
	done := make(chan struct{})
	go func() {
		_ = client.Wait()
		close(done)
	}()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			if _, _, err := client.SendRequest("keepalive@openssh.com", true, nil); err != nil {
				return
			}
		}
	}
}

func (ex *exitClient) sleep(d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			return !ex.isClosed()
		case <-time.After(200 * time.Millisecond):
			if ex.isClosed() {
				return false
			}
		}
	}
}

func (ex *exitClient) current() *gossh.Client {
	ex.mu.Lock()
	defer ex.mu.Unlock()
	return ex.client
}

func (ex *exitClient) serveSocks() {
	defer ex.wg.Done()
	for {
		conn, err := ex.ln.Accept()
		if err != nil {
			return
		}
		ex.wg.Add(1)
		go func() {
			defer ex.wg.Done()
			ex.handleSocks(conn)
		}()
	}
}

const (
	socksVersion       = 5
	socksNoAuth        = 0
	socksCmdConnect    = 1
	socksAtypIPv4      = 1
	socksAtypDomain    = 3
	socksAtypIPv6      = 4
	socksRepOK         = 0
	socksRepFailure    = 1
	socksRepUnreach    = 4
	socksRepCmdUnknown = 7
)

// handleSocks speaks just enough SOCKS5 for a CONNECT from Xray's socks
// outbound: no auth, no BIND, no UDP ASSOCIATE — the exit is TCP only.
func (ex *exitClient) handleSocks(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(exitDialTimeout))
	head := make([]byte, 2)
	if _, err := io.ReadFull(conn, head); err != nil || head[0] != socksVersion {
		return
	}
	methods := make([]byte, int(head[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return
	}
	if _, err := conn.Write([]byte{socksVersion, socksNoAuth}); err != nil {
		return
	}
	req := make([]byte, 4)
	if _, err := io.ReadFull(conn, req); err != nil || req[0] != socksVersion {
		return
	}
	var host string
	switch req[3] {
	case socksAtypIPv4:
		buf := make([]byte, 4)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return
		}
		host = net.IP(buf).String()
	case socksAtypDomain:
		n := make([]byte, 1)
		if _, err := io.ReadFull(conn, n); err != nil {
			return
		}
		buf := make([]byte, int(n[0]))
		if _, err := io.ReadFull(conn, buf); err != nil {
			return
		}
		host = string(buf)
	case socksAtypIPv6:
		buf := make([]byte, 16)
		if _, err := io.ReadFull(conn, buf); err != nil {
			return
		}
		host = net.IP(buf).String()
	default:
		socksReply(conn, socksRepFailure)
		return
	}
	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBuf); err != nil {
		return
	}
	if req[1] != socksCmdConnect {
		socksReply(conn, socksRepCmdUnknown)
		return
	}
	client := ex.current()
	if client == nil {
		socksReply(conn, socksRepUnreach)
		return
	}
	dst, err := client.Dial("tcp", net.JoinHostPort(host, strconv.Itoa(int(binary.BigEndian.Uint16(portBuf)))))
	if err != nil {
		socksReply(conn, socksRepUnreach)
		return
	}
	defer dst.Close()
	if !socksReply(conn, socksRepOK) {
		return
	}
	_ = conn.SetDeadline(time.Time{})
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(dst, conn); done <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, dst); done <- struct{}{} }()
	<-done
}

func socksReply(conn net.Conn, rep byte) bool {
	_, err := conn.Write([]byte{socksVersion, rep, 0, socksAtypIPv4, 0, 0, 0, 0, 0, 0})
	return err == nil
}
