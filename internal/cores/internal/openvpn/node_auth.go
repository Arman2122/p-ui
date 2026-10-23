package openvpn

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

var ErrNodeManagement = errors.New("OpenVPN management contract failed")

var (
	errNodeCredentials   = errors.New("client authentication failed")
	errNodeAuthorization = errors.New("client authorization unavailable")
)

type (
	nodeSession struct {
		user        string
		credential  *nodeAuthentication
		address     netip.Addr
		created     time.Time
		established bool
		killing     bool
	}
	nodeEvent struct {
		kind     string
		cid, kid uint32
		env      map[string]string
		size     int
	}
	nodeCommand struct {
		kind string
		cid  uint32
	}
	nodeAuthentication struct {
		client NodeClient
		proof  *admission.BoundProof
		scoped bool
	}
)

type NodeAuthenticator struct {
	mu       sync.Mutex
	config   NodeConfiguration
	users    map[string]*nodeAuthentication
	grants   map[string]governor.Grant
	sessions map[uint32]nodeSession
	used     map[netip.Addr]bool
	clock    limits.Clock
}

func (a *NodeAuthenticator) SetClock(clock limits.Clock) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.clock = clock
}

func NewNodeAuthenticator(config NodeConfiguration) *NodeAuthenticator {
	a := &NodeAuthenticator{config: config, users: make(map[string]*nodeAuthentication, len(config.Clients)), grants: map[string]governor.Grant{}, sessions: map[uint32]nodeSession{}, used: map[netip.Addr]bool{}}
	for _, client := range config.Clients {
		client.Addresses = slices.Clone(client.Addresses)
		a.users[client.Username] = &nodeAuthentication{client: client}
	}
	return a
}

func (a *NodeAuthenticator) BindAdmission(binding *admission.Binding, tag string) error {
	if binding == nil || tag == "" {
		return admission.ErrUnavailable
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	next := make(map[string]*nodeAuthentication, len(a.users))
	for name, old := range a.users {
		proof, found := binding.Capture(old.client.ClientID, tag)
		if !found {
			return admission.ErrUnavailable
		}
		next[name] = old
		if !old.scoped || old.proof != proof {
			next[name] = &nodeAuthentication{client: old.client, proof: proof, scoped: true}
		}
	}
	a.users = next
	return nil
}

func (c *nodeAuthentication) current() bool {
	return c != nil && (!c.scoped || c.proof.Current())
}

func (a *NodeAuthenticator) Grants(grants []governor.Grant) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.grants = make(map[string]governor.Grant, len(grants))
	for _, grant := range grants {
		a.grants[grant.ClientID] = grant
	}
}

func (a *NodeAuthenticator) authorize(ctx context.Context, e nodeEvent, now time.Time) (netip.Addr, error) {
	a.mu.Lock()
	credential, clock := a.users[e.env["username"]], a.clock
	a.mu.Unlock()
	if credential == nil {
		return netip.Addr{}, errNodeCredentials
	}
	provided, expected := sha256.Sum256([]byte(e.env["password"])), sha256.Sum256([]byte(credential.client.Password))
	if subtle.ConstantTimeCompare(provided[:], expected[:]) != 1 {
		return netip.Addr{}, errNodeCredentials
	}
	authorizedAt := now
	if clock != nil {
		var err error
		authorizedAt, err = clock.Now()
		if err != nil {
			return netip.Addr{}, errNodeAuthorization
		}
	}
	a.mu.Lock()
	if a.users[e.env["username"]] != credential || !credential.current() || ctx.Err() != nil {
		a.mu.Unlock()
		return netip.Addr{}, errNodeAuthorization
	}
	user := credential.client
	grant, ok := a.grants[user.ClientID]
	if !ok || !grant.Policy.Enabled || grant.Policy.ExpiresUnixMilli != 0 && grant.Policy.ExpiresUnixMilli <= authorizedAt.UnixMilli() ||
		credential.scoped && (grant.Policy.Upload.Mode == limits.Blocked || grant.Policy.Download.Mode == limits.Blocked || !grant.Policy.QuotaUnlimited && grant.Policy.CreditCeiling == 0) {
		a.mu.Unlock()
		if credential.scoped {
			_ = credential.proof.Request(ctx)
		}
		return netip.Addr{}, errNodeAuthorization
	}
	defer a.mu.Unlock()
	if session, exists := a.sessions[e.cid]; exists {
		if e.kind == "REAUTH" && session.credential == credential && !session.killing {
			return session.address, nil
		}
		return netip.Addr{}, errNodeAuthorization
	}
	if e.kind != "CONNECT" {
		return netip.Addr{}, errNodeAuthorization
	}
	for _, address := range user.Addresses {
		if !a.used[address] {
			a.used[address] = true
			a.sessions[e.cid] = nodeSession{user: user.Username, credential: credential, address: address, created: now}
			return address, nil
		}
	}
	return netip.Addr{}, errNodeAuthorization
}

func (a *NodeAuthenticator) close(cid uint32) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if session, ok := a.sessions[cid]; ok {
		delete(a.used, session.address)
		delete(a.sessions, cid)
	}
}

func (a *NodeAuthenticator) event(e nodeEvent) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if e.kind == "ESTABLISHED" {
		session, exists := a.sessions[e.cid]
		if !exists || e.env["common_name"] != session.user || a.users[session.user] != session.credential || !session.credential.current() {
			return ErrNodeManagement
		}
		session.established = true
		a.sessions[e.cid] = session
	}
	return nil
}

func (a *NodeAuthenticator) stale(now time.Time) []uint32 {
	a.mu.Lock()
	defer a.mu.Unlock()
	var stale []uint32
	for cid, session := range a.sessions {
		if !session.established && !session.killing && now.Sub(session.created) > 90*time.Second {
			session.killing = true
			a.sessions[cid] = session
			stale = append(stale, cid)
		}
	}
	return stale
}

func parseNodeEvent(line string) (nodeEvent, error) {
	fields := strings.Split(strings.TrimPrefix(line, ">CLIENT:"), ",")
	e := nodeEvent{env: map[string]string{}}
	if len(fields) < 2 {
		return e, ErrNodeManagement
	}
	e.kind = fields[0]
	cid, err := strconv.ParseUint(fields[1], 10, 32)
	if err != nil {
		return e, ErrNodeManagement
	}
	e.cid = uint32(cid)
	switch e.kind {
	case "CONNECT", "REAUTH":
		if len(fields) != 3 {
			return e, ErrNodeManagement
		}
		kid, err := strconv.ParseUint(fields[2], 10, 32)
		if err != nil {
			return e, ErrNodeManagement
		}
		e.kid = uint32(kid)
	case "DISCONNECT", "ESTABLISHED":
		if len(fields) != 2 {
			return e, ErrNodeManagement
		}
	case "ADDRESS":
		if len(fields) != 4 || fields[3] != "1" {
			return e, ErrNodeManagement
		}
		e.env["address"] = fields[2]
	default:
		return e, ErrNodeManagement
	}
	return e, nil
}

func (a *NodeAuthenticator) Run(ctx context.Context, conn net.Conn, ready chan<- struct{}) error {
	defer conn.Close()
	lines, readErr, stopped := make(chan string), make(chan error, 1), make(chan struct{})
	defer close(stopped)
	go func() {
		scanner := bufio.NewScanner(conn)
		scanner.Buffer(make([]byte, 4096), 8192)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-stopped:
				return
			}
		}
		err := scanner.Err()
		if err == nil {
			err = io.EOF
		}
		readErr <- err
	}()
	var pending []nodeCommand
	write := func(command nodeCommand, format string, args ...any) error {
		if len(pending) >= 4096 {
			return ErrNodeManagement
		}
		if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(conn, format, args...); err != nil {
			return err
		}
		pending = append(pending, command)
		return nil
	}
	if err := write(nodeCommand{}, "state on\n"); err != nil {
		return err
	}
	if err := write(nodeCommand{}, "hold release\n"); err != nil {
		return err
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	var event *nodeEvent
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-readErr:
			return err
		case now := <-ticker.C:
			for _, cid := range a.stale(now) {
				if err := write(nodeCommand{kind: "kill", cid: cid}, "client-kill %d\n", cid); err != nil {
					return err
				}
			}
		case line := <-lines:
			if strings.HasPrefix(line, "ERROR:") || strings.HasPrefix(line, ">FATAL:") {
				return ErrNodeManagement
			}
			if strings.HasPrefix(line, "SUCCESS:") {
				if len(pending) == 0 {
					return ErrNodeManagement
				}
				if pending[0].kind == "kill" {
					a.close(pending[0].cid)
				}
				pending = pending[1:]
				continue
			}
			if strings.HasPrefix(line, ">STATE:") && strings.Contains(line, ",CONNECTED,SUCCESS,") && ready != nil {
				close(ready)
				ready = nil
			}
			if !strings.HasPrefix(line, ">CLIENT:") {
				continue
			}
			if strings.HasPrefix(line, ">CLIENT:ENV,") {
				if event == nil {
					return ErrNodeManagement
				}
				value := strings.TrimPrefix(line, ">CLIENT:ENV,")
				event.size += len(value)
				if event.size > 64<<10 {
					return ErrNodeManagement
				}
				if value != "END" {
					name, value, ok := strings.Cut(value, "=")
					if !ok {
						return ErrNodeManagement
					}
					if name == "username" || name == "password" || name == "common_name" {
						if _, duplicate := event.env[name]; duplicate {
							return ErrNodeManagement
						}
						event.env[name] = value
					}
					continue
				}
				switch event.kind {
				case "CONNECT", "REAUTH":
					address, authErr := a.authorize(ctx, *event, time.Now())
					if authErr == nil {
						if err := write(nodeCommand{}, "client-auth %d %d\nifconfig-push %s %s\nEND\n", event.cid, event.kid, address, a.config.Netmask()); err != nil {
							return err
						}
					} else {
						reason := "denied"
						if errors.Is(authErr, errNodeAuthorization) {
							reason = "authorization-unavailable"
						}
						if err := write(nodeCommand{}, "client-deny %d %d %s\n", event.cid, event.kid, reason); err != nil {
							return err
						}
					}
				case "DISCONNECT":
					a.close(event.cid)
				case "ESTABLISHED":
					if err := a.event(*event); err != nil {
						return err
					}
				}
				event = nil
				continue
			}
			if event != nil {
				return ErrNodeManagement
			}
			e, err := parseNodeEvent(line)
			if err != nil {
				return err
			}
			if e.kind == "ADDRESS" {
				a.mu.Lock()
				session, ok := a.sessions[e.cid]
				ok = ok && a.users[session.user] == session.credential && session.credential.current()
				a.mu.Unlock()
				if !ok || session.address.String() != e.env["address"] {
					return ErrNodeManagement
				}
				continue
			}
			event = &e
		}
	}
}
