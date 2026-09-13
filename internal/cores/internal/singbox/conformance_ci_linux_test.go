//go:build linux

package singbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/core/coretest"
	"github.com/Arman2122/p-ui/internal/supervisor"
)

// fakeSingBox answers what the core asks a binary outside a running daemon:
// its version, and a config check that passes.
func fakeSingBox(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sing-box")
	script := "#!/bin/sh\ncase \"$1\" in version) echo \"sing-box version " + Version + "\";; esac\nexit 0\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// usersInFile is what the fake daemon "serves": the users in the config file
// the core landed, which is all a daemon that never runs can be held to.
func (c *Core) usersInFile(t *testing.T) []string {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, cur := range c.instances {
		if !cur.running {
			continue
		}
		body, err := os.ReadFile(cur.cfgPath)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Inbounds []struct {
				Users []struct {
					Name string `json:"name"`
				} `json:"users"`
			} `json:"inbounds"`
		}
		if err := json.Unmarshal(body, &doc); err != nil {
			t.Fatal(err)
		}
		for _, ib := range doc.Inbounds {
			for _, u := range ib.Users {
				if !slices.Contains(out, u.Name) {
					out = append(out, u.Name)
				}
			}
		}
	}
	return out
}

/*
The conformance suite without the daemon, so CI holds the contract on every
commit: the unit backend is a ledger, the binary a script, and the daemon's
four read seams answer what the rig feeds. The real-daemon run stays gated on
SINGBOX_E2E_BINARY.
*/
func TestConformanceWithoutTheDaemon(t *testing.T) {
	bin := fakeSingBox(t)
	releases := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("[]")) }))
	t.Cleanup(releases.Close)
	previousURL := releasesURL
	releasesURL = releases.URL
	t.Cleanup(func() { releasesURL = previousURL })
	certFile, keyFile := selfSigned(t, t.TempDir())
	port := freePort(t)
	ledger := newUnitLedger()

	var mu sync.Mutex
	fed := map[string][2]int64{}
	var sessions []liveConnection
	prevStats, prevConns, prevClose, prevStarted := readUserStats, readConnections, closeConn, readStartedAt
	t.Cleanup(func() {
		readUserStats, readConnections, closeConn, readStartedAt = prevStats, prevConns, prevClose, prevStarted
	})
	readUserStats = func(context.Context, int) (map[string]int64, error) {
		mu.Lock()
		defer mu.Unlock()
		out := map[string]int64{}
		for email, v := range fed {
			out["user>>>"+email+">>>traffic>>>uplink"] = v[0]
			out["user>>>"+email+">>>traffic>>>downlink"] = v[1]
		}
		return out, nil
	}
	readConnections = func(context.Context, int, string) ([]liveConnection, error) {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(sessions), nil
	}
	closeConn = func(_ context.Context, _ int, _ string, id string) error {
		mu.Lock()
		defer mu.Unlock()
		sessions = slices.DeleteFunc(sessions, func(s liveConnection) bool { return s.id == id })
		return nil
	}
	// The start time moves on every reload; that is what the reload wait watches.
	readStartedAt = func(context.Context, int, string) (int64, error) {
		ledger.mu.Lock()
		defer ledger.mu.Unlock()
		return int64(ledger.reloads) + 1, nil
	}

	stateDir := t.TempDir()
	var current *Core
	rig := coretest.Rig{
		NewCore: func() (core.Core, error) {
			c := New(Deps{Backend: ledger, Binary: bin, StateDir: stateDir, ApplyDelay: time.Hour, Platform: "amd64", HTTPClient: func(time.Duration) *http.Client { return releases.Client() }})
			if current == nil {
				current = c
			}
			return c, nil
		},
		Instance: func(users int) core.Instance {
			inst := core.Instance{ID: 1, Kind: KindTUIC, Tag: "tuic-1", Listen: "127.0.0.1", Port: port, Enable: true, Settings: settingsJSON(t, certFile, keyFile)}
			for i := 0; i < users; i++ {
				inst.Users = append(inst.Users, e2eUser(i))
			}
			return inst
		},
		Starts: func() int { current.mu.Lock(); defer current.mu.Unlock(); return current.starts },
		Alive:  func() bool { return ledger.has(supervisor.UnitName(string(ID), 1)) },
		FeedTraffic: func(email string, up, down int64) {
			mu.Lock()
			fed[email] = [2]int64{up, down}
			mu.Unlock()
		},
		RestartSource: func() {
			mu.Lock()
			fed = map[string][2]int64{}
			mu.Unlock()
			current.mu.Lock()
			for _, cur := range current.instances {
				cur.epoch = newEpoch()
			}
			current.mu.Unlock()
		},
		ServedUsers: func() []string { return current.usersInFile(t) },
		Settle:      func() { current.settleNow(t) },
		FeedSession: func(email, source string, lastSeen int64) {
			mu.Lock()
			defer mu.Unlock()
			conn := liveConnection{id: fmt.Sprintf("%s|%s|%d", email, source, lastSeen), user: email, source: netip.MustParseAddr(source), createdAt: lastSeen}
			for i := range sessions {
				if sessions[i].user == email && sessions[i].source.String() == source {
					sessions[i] = conn
					return
				}
			}
			sessions = append(sessions, conn)
		},
	}
	for _, failure := range coretest.Check(rig) {
		t.Error(failure)
	}
	if current != nil {
		_ = current.StopAll(context.Background())
	}
}
