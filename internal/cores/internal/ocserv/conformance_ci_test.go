//go:build linux

package ocserv

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/core/coretest"
	"github.com/Arman2122/p-ui/internal/radius"
	"github.com/Arman2122/p-ui/internal/supervisor"
)

// fakeOcserv answers the version probe and the config test; occtl is never
// run, its answers come from the seams the rig swaps.
func fakeOcserv(t *testing.T) (daemon, occtl string) {
	t.Helper()
	dir := t.TempDir()
	daemon, occtl = filepath.Join(dir, "ocserv"), filepath.Join(dir, "occtl")
	script := "#!/bin/sh\ncase \"$1\" in --version) echo \"OpenConnect VPN Server 1.2.4\";; esac\nexit 0\n"
	for _, path := range []string{daemon, occtl} {
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return daemon, occtl
}

/*
The conformance suite without the daemon, so CI holds the contract on every
commit: the unit backend is a ledger, RADIUS is the panel's own server in
process, and occtl's three answers come from the sessions the rig feeds. A
cut drops the session the way the daemon would, with an accounting Stop.
*/
func TestConformanceWithoutTheDaemon(t *testing.T) {
	daemon, occtl := fakeOcserv(t)
	ledger := newUnitLedger()
	srv, err := radius.New(radius.NewSecret(), freeUDPAddr(t), freeUDPAddr(t))
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

	var mu sync.Mutex
	rows := map[string]userRow{}
	nextID := 0
	restarted := map[string]bool{}
	generation := map[string]int{}
	fedBytes := map[string]int64{}
	sessionOf := func(email string) string { return fmt.Sprintf("fed-%s-%d", email, generation[email]) }
	deviceSession := func(email, source string) string { return fmt.Sprintf("dev-%s-%s", email, source) }
	var current *Core

	prevList, prevCut, prevAnswers := listUsers, disconnectSource, daemonAnswers
	t.Cleanup(func() { listUsers, disconnectSource, daemonAnswers = prevList, prevCut, prevAnswers })
	daemonAnswers = func(context.Context, string, string) error { return nil }
	listUsers = func(context.Context, string, string) ([]userRow, error) {
		mu.Lock()
		defer mu.Unlock()
		out := make([]userRow, 0, len(rows))
		for _, row := range rows {
			out = append(out, row)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
		return out, nil
	}
	disconnectSource = func(_ context.Context, _, _, username string, source netip.Addr) (int, error) {
		mu.Lock()
		defer mu.Unlock()
		cut := 0
		for key, row := range rows {
			if row.Username != username || row.RemoteIP != source.String() {
				continue
			}
			delete(rows, key)
			cut++
			email := emailOf[username]
			current.mu.Lock()
			for _, cur := range current.instances {
				cur.ledger.Apply(radius.Event{Status: radius.Stop, SessionID: deviceSession(email, source.String()), Username: email})
			}
			current.mu.Unlock()
		}
		return cut, nil
	}

	var settings string
	rig := coretest.Rig{
		NewCore: func() (core.Core, error) {
			c := New(Deps{Radius: srv, Backend: ledger, Binary: daemon, Occtl: occtl, StateDir: stateDir})
			if current == nil {
				current = c
				settings = mintedSettings(t, c, "10.219.0.0/24")
			}
			return c, nil
		},
		Instance: func(users int) core.Instance {
			inst := core.Instance{ID: 1, Kind: Kind, Tag: "ocs-1", Listen: "127.0.0.1", Port: 24443, Enable: true, Settings: settings}
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
			mu.Lock()
			rows = map[string]userRow{}
			mu.Unlock()
			for _, u := range candidates {
				restarted[u.Email] = true
				generation[u.Email]++
			}
		},
		ServedUsers: func() []string { return current.servedByDaemon(t, candidates) },
		HostSubjects: func() map[string][]string {
			mu.Lock()
			defer mu.Unlock()
			out := map[string][]string{}
			for _, row := range rows {
				out[emailOf[row.Username]] = append(out[emailOf[row.Username]], row.IPv4+"/32")
			}
			return out
		},
		FeedSession: func(email, source string, lastSeen int64) {
			mu.Lock()
			key := email + "|" + source
			if _, ok := rows[key]; !ok {
				nextID++
				var username string
				for name, e := range emailOf {
					if e == email {
						username = name
					}
				}
				rows[key] = userRow{ID: json.Number(strconv.Itoa(nextID)), Username: username, RemoteIP: source, Device: "poc1-" + strconv.Itoa(nextID), IPv4: "10.219.0." + strconv.Itoa(nextID+1)}
			}
			mu.Unlock()
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
