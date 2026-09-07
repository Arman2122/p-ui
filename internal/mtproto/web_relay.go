package mtproto

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/Arman2122/p-ui/internal/logger"
)

// The WEB relay: mtg-proxy's second process, serving Telegram Desktop's WEB
// proxy off the same config file as the proxy. The panel owns its token key,
// its loopback ports and its lifetime; the operator brings the hostname's
// certificate, the front proxy on 443 that speaks PROXY v2 to it, and a cover
// site, none of which the panel can provide.

// webKeyPath is the relay's token signing key for an inbound, beside its config.
func webKeyPath(id int) string {
	return fmt.Sprintf("%s/web-%d.key", configDir(), id)
}

// ensureWebKey writes the 32-byte signing key once and never again: a rotated
// key invalidates every live WEB session, and the relay refuses one readable by
// anyone but its owner.
func ensureWebKey(id int) error {
	path := webKeyPath(id)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.MkdirAll(configDir(), 0o750); err != nil {
		return err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return err
	}
	return os.WriteFile(path, key, 0o600)
}

// webRelayCheck runs the relay's own validation over the rendered file, so a
// [web] table it would refuse is refused before a process is started for it.
func webRelayCheck(configPath string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, GetBinaryPath(), "web-relay", "--check", configPath).CombinedOutput()
	if err != nil {
		if last := lastLine(string(out)); last != "" {
			return errors.New(last)
		}
		return err
	}
	return nil
}

func lastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// ensureRelayLocked converges the relay beside a running proxy: started when the
// inbound wants one and none runs, left alone while it runs, stopped when the
// inbound no longer wants it. Called with m.mu held, after the proxy is up.
func (m *Manager) ensureRelayLocked(cur *managed, inst Instance) {
	if !inst.webRelayConfigured() {
		cur.stopRelay()
		cur.relayCheck = ""
		return
	}
	if cur.relay != nil && cur.relay.IsRunning() {
		return
	}
	cfgPath := configPathForID(inst.Id)
	if err := ensureWebKey(inst.Id); err != nil {
		cur.relayCheck = err.Error()
		logger.Warningf("mtproto: WEB relay for inbound %d has no signing key: %v", inst.Id, err)
		return
	}
	if err := webRelayCheck(cfgPath); err != nil {
		cur.relayCheck = err.Error()
		logger.Warningf("mtproto: WEB relay for inbound %d refused its config: %v", inst.Id, err)
		return
	}
	relay := newRelayProcess(cfgPath, fmt.Sprintf("web relay for inbound %d", inst.Id), inst.Id)
	relay.keepLines(cur.ring)
	if err := relay.Start(); err != nil {
		cur.relayCheck = err.Error()
		logger.Warningf("mtproto: WEB relay for inbound %d did not start: %v", inst.Id, err)
		return
	}
	cur.relay, cur.relayStats, cur.relayCheck = relay, inst.WebStatsPort, ""
	logger.Infof("mtproto: started the WEB relay for inbound %d on 127.0.0.1:%d for %s", inst.Id, inst.WebBindPort, inst.WebHost)
}

// stopRelay ends the relay if one runs; it is safe on a record without one.
func (cur *managed) stopRelay() {
	if cur.relay != nil && cur.relay.IsRunning() {
		_ = cur.relay.Stop()
	}
	cur.relay = nil
}

// reloadRelay asks a running relay to re-read the file now. The relay watches
// the file anyway, so a platform with no SIGHUP loses only the immediacy.
func (cur *managed) reloadRelay() {
	if cur.relay != nil {
		_ = cur.relay.Signal(syscall.SIGHUP)
	}
}

// relayHealth is why a relay is not healthy, or "" when it runs and its stats
// socket answers; a relay with no stats socket is healthy when it runs.
func relayHealth(relay *Process, statsPort int) string {
	switch {
	case relay.IsRunning():
	case relay.Crashed():
		return "exited: " + relay.GetResult()
	default:
		return "stopped"
	}
	if statsPort <= 0 {
		return ""
	}
	if _, ok := scrapeRelayStats(statsPort); !ok {
		return "the stats socket does not answer"
	}
	return ""
}

// RelayStats is the relay's own counters, the part of them a panel reads.
type RelayStats struct {
	Profiles []string `json:"profiles"`
	Reloads  int64    `json:"reloads"`
	Sessions struct {
		Active int64 `json:"active"`
	} `json:"sessions"`
	Refused struct {
		CoverServed int64 `json:"cover_served"`
	} `json:"refused"`
}

// scrapeRelayStats reads the relay's loopback stats socket, which carries no
// credential by design: it is loopback only, and the relay refuses otherwise.
func scrapeRelayStats(port int) (RelayStats, bool) {
	client := http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/stats", port), nil)
	if err != nil {
		return RelayStats{}, false
	}
	resp, err := client.Do(req)
	if err != nil {
		return RelayStats{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return RelayStats{}, false
	}
	var parsed RelayStats
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return RelayStats{}, false
	}
	return parsed, true
}

// ValidateWebRelaySettings refuses what the relay's own ValidateWeb refuses,
// with the rule named, so a half-configured relay is a save error and not an
// inbound whose second process never comes up.
func ValidateWebRelaySettings(settings string) error {
	if settings == "" {
		return nil
	}
	var parsed instanceSettings
	if err := json.Unmarshal([]byte(settings), &parsed); err != nil {
		return err
	}
	if !parsed.WebEnable {
		return nil
	}
	host := strings.TrimSpace(parsed.WebHost)
	switch {
	case host == "":
		return errors.New("the WEB relay needs a hostname: the name clients type, with a browser-trusted certificate on port 443")
	case strings.Contains(host, "/") || strings.Contains(host, ":") || strings.Contains(host, " "):
		return errors.New("the WEB hostname must be a bare hostname, not a URL or an address with a port")
	case !strings.Contains(host, "."):
		return errors.New("the WEB hostname must be a fully qualified name a public certificate can be issued for")
	}
	dir, upstream := strings.TrimSpace(parsed.WebCoverDir), strings.TrimSpace(parsed.WebCoverUpstream)
	if (dir == "") == (upstream == "") {
		return errors.New("the WEB relay needs exactly one cover site: a directory of files, or an http upstream to reverse-proxy")
	}
	if upstream != "" {
		u, err := url.Parse(upstream)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("the WEB cover upstream must be an http:// or https:// URL")
		}
	}
	return nil
}
