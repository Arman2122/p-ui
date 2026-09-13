//go:build linux

package openvpn

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/core/coretest"
	"github.com/Arman2122/p-ui/internal/radius"
	"github.com/Arman2122/p-ui/internal/supervisor"
)

// fakeOpenVPN answers the two things the core runs the binary for outside a
// daemon: its version, and minting a client's tls-crypt-v2 key into a file.
func fakeOpenVPN(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "openvpn")
	script := "#!/bin/sh\ncase \"$1\" in\n" +
		"  --version) echo \"OpenVPN 2.6.12 x86_64-pc-linux-gnu [SSL (OpenSSL)] built on Jan 1 2026\";;\n" +
		"  --tls-crypt-v2) printf -- '-----BEGIN OpenVPN tls-crypt-v2 client key-----\\nZmFrZQ==\\n-----END OpenVPN tls-crypt-v2 client key-----\\n' > \"$5\";;\n" +
		"esac\nexit 0\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// fakeManagement is the daemon's management socket for the two verbs the core
// speaks: `status 3` lists the sessions the rig fed, `kill` ends one.
type fakeManagement struct {
	mu      sync.Mutex
	clients map[string][]string
	onKill  func(commonName, address string)
}

func (m *fakeManagement) serve(t *testing.T, sock string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(sock), 0o770); err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go m.handle(c)
		}
	}()
}

func (m *fakeManagement) handle(c net.Conn) {
	defer c.Close()
	_, _ = fmt.Fprint(c, ">INFO:OpenVPN Management Interface Version 5 -- type 'help' for more info\n")
	r := bufio.NewReader(c)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		switch line = strings.TrimSpace(line); {
		case line == "status 3":
			m.mu.Lock()
			for cn, addrs := range m.clients {
				for _, a := range addrs {
					_, _ = fmt.Fprintf(c, "CLIENT_LIST\t%s\t%s\t10.213.0.2\t\t0\t0\t2026-01-01 00:00:00\t0\t%s\t0\t0\n", cn, a, cn)
				}
			}
			m.mu.Unlock()
			_, _ = fmt.Fprint(c, "END\n")
		case strings.HasPrefix(line, "kill "):
			target := strings.TrimPrefix(line, "kill ")
			killed := 0
			m.mu.Lock()
			for cn, addrs := range m.clients {
				kept := addrs[:0]
				for _, a := range addrs {
					if a == target || cn == target {
						killed++
						m.onKill(cn, a)
						continue
					}
					kept = append(kept, a)
				}
				m.clients[cn] = kept
			}
			m.mu.Unlock()
			if killed == 0 {
				_, _ = fmt.Fprintf(c, "ERROR: common name '%s' not found\n", target)
			} else {
				_, _ = fmt.Fprintf(c, "SUCCESS: %d client(s) killed\n", killed)
			}
		default:
			_, _ = fmt.Fprint(c, "ERROR: unknown command\n")
		}
	}
}

/*
The conformance suite without the daemon, so CI holds the contract on every
commit: the unit backend is a ledger, RADIUS is the panel's own server in
process, the binary a script, and the management socket a stand-in that lists
and kills the sessions the rig fed, sending the Stop the daemon would.
*/
func TestConformanceWithoutTheDaemon(t *testing.T) {
	bin := fakeOpenVPN(t)
	ledger := newUnitLedger()
	// The daemon's RADIUS plugin cannot name a port above 9999, so the panel's
	// server takes two low ones, as the real-daemon rig does.
	srv, err := radius.New(radius.NewSecret(), freeLowUDP(t, 11), freeLowUDP(t, 29))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = srv.Close() })
	stateDir := t.TempDir()
	candidates := []core.User{e2eUser(0), e2eUser(1), e2eUser(2)}
	emailOf := map[string]string{}
	for _, u := range candidates {
		emailOf[core.CredString(u.Credentials, core.CredUsername)] = u.Email
	}
	restarted := map[string]bool{}
	generation := map[string]int{}
	fedBytes := map[string]int64{}
	sessionOf := func(email string) string { return fmt.Sprintf("fed-%s-%d", email, generation[email]) }
	deviceSession := func(email, source string) string { return fmt.Sprintf("dev-%s-%s", email, source) }
	var current *Core
	mgmt := &fakeManagement{clients: map[string][]string{}}
	mgmt.onKill = func(cn, address string) {
		host, _, _ := net.SplitHostPort(address)
		email := emailOf[cn]
		current.mu.Lock()
		for _, cur := range current.instances {
			cur.ledger.Apply(radius.Event{Status: radius.Stop, SessionID: deviceSession(email, host), Username: email})
		}
		current.mu.Unlock()
	}
	mgmt.serve(t, pathsIn(filepath.Join(stateDir, "1")).mgmt)

	var settings string
	rig := coretest.Rig{
		NewCore: func() (core.Core, error) {
			c := New(Deps{Radius: srv, Backend: ledger, Binary: bin, StateDir: stateDir})
			if current == nil {
				current = c
				settings = mintedSettings(t, c, "10.213.0.0/24", ProtoUDP)
				for i := range candidates {
					have := map[string]string{core.CredUsername: core.CredString(candidates[i].Credentials, core.CredUsername), core.CredPassword: core.CredString(candidates[i].Credentials, core.CredPassword)}
					minted, err := c.MintClientCredentials(Kind, settings, have)
					if err != nil {
						t.Fatal(err)
					}
					candidates[i].Credentials[core.CredTLSCryptV2] = minted[core.CredTLSCryptV2]
				}
			}
			return c, nil
		},
		Instance: func(users int) core.Instance {
			inst := core.Instance{ID: 1, Kind: Kind, Tag: "ovpn-1", Listen: "127.0.0.1", Port: 21194, Enable: true, Settings: settings}
			for i := 0; i < users; i++ {
				inst.Users = append(inst.Users, candidates[i])
			}
			return inst
		},
		Starts: func() int { current.mu.Lock(); defer current.mu.Unlock(); return current.starts },
		Alive:  func() bool { return ledger.has(supervisor.UnitName(string(ID), 1)) },
		FeedTraffic: func(email string, up, down int64) {
			current.mu.Lock()
			for _, cur := range current.instances {
				if restarted[email] {
					cur.ledger.Apply(radius.Event{Status: radius.Start, SessionID: sessionOf(email), Username: email})
					delete(restarted, email)
				}
				cur.ledger.Apply(radius.Event{Status: radius.Interim, SessionID: sessionOf(email), Username: email, In: up, Out: down})
			}
			current.mu.Unlock()
		},
		RestartSource: func() {
			current.mu.Lock()
			for _, cur := range current.instances {
				cur.ledger = radius.NewLedger(cur.inst.Tag)
			}
			current.mu.Unlock()
			mgmt.mu.Lock()
			mgmt.clients = map[string][]string{}
			mgmt.mu.Unlock()
			for _, u := range candidates {
				restarted[u.Email] = true
				generation[u.Email]++
			}
		},
		ServedUsers:  func() []string { return current.servedByDaemon(t, candidates) },
		HostSubjects: func() map[string][]string { return current.ccdSubjects(t, candidates) },
		FeedSession: func(email, source string, lastSeen int64) {
			var username string
			for name, e := range emailOf {
				if e == email {
					username = name
				}
			}
			address := net.JoinHostPort(source, strconv.Itoa(40000+len(source)))
			mgmt.mu.Lock()
			if !containsString(mgmt.clients[username], address) {
				mgmt.clients[username] = append(mgmt.clients[username], address)
			}
			mgmt.mu.Unlock()
			current.mu.Lock()
			for _, cur := range current.instances {
				cur.ledger.SetClock(func() time.Time { return time.UnixMilli(lastSeen) })
				fedBytes[email]++
				cur.ledger.Apply(radius.Event{Status: radius.Interim, SessionID: deviceSession(email, source), Username: email, Peer: radius.Peer{Source: netip.MustParseAddr(source)}, In: fedBytes[email]})
				cur.ledger.SetClock(nil)
			}
			current.mu.Unlock()
		},
	}
	for _, failure := range coretest.Check(rig) {
		t.Error(failure)
	}
	if current != nil {
		_ = current.StopAll(context.Background())
	}
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
