package mtproto

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/supervisor"
	"github.com/Arman2122/p-ui/internal/util/sys"
)

// SecretEntry is one named FakeTLS secret served by an mtg-proxy process. Name is
// the client email, used both as the [secrets] key and as the per-user key in the
// /stats API so traffic can be attributed back to the client. AdTag is the
// client's own advertising-tag override, emitted into the [secret-ad-tags]
// section; empty falls back to the instance-level tag.
type SecretEntry struct {
	Name        string
	Secret      string
	AdTag       string
	QuotaBytes  int64
	ExpiresUnix int64
	// SpeedUpBytesPerSecond and SpeedDownBytesPerSecond are in the sidecar's
	// unit, bytes per second from the client's side; IPLimit is how many places
	// the client may hold at once. Zero is unlimited for each.
	SpeedUpBytesPerSecond   int64
	SpeedDownBytesPerSecond int64
	IPLimit                 int
}

// Instance is the desired runtime configuration of one mtproto inbound. A single
// mtg-proxy process serves every active client's secret through the [secrets]
// section, so one inbound maps to one process with many named secrets.
type Instance struct {
	Id      int
	Tag     string
	Listen  string
	Port    int
	Secrets []SecretEntry

	// Optional mtg tuning; each is omitted from the generated TOML when
	// zero-valued so mtg falls back to its own defaults.
	Debug                 bool
	ProxyProtocolListener bool
	PreferIP              string
	FrontingIP            string
	FrontingPort          int
	FrontingProxyProtocol bool

	// ThrottleMaxConnections caps concurrent connections across all users with a
	// fair-share algorithm; zero disables throttling.
	ThrottleMaxConnections int

	// PublicIPv4/PublicIPv6 pin the proxy's reachable address the Telegram
	// middle proxy needs when clients carry advertising tags; they are omitted
	// when empty so mtg auto-detects, and a change forces a restart.
	PublicIPv4 string
	PublicIPv6 string

	// When RouteThroughXray is set, mtg dials Telegram through the loopback
	// SOCKS bridge the panel injects into the Xray config at XrayRoutePort, so
	// the egress obeys the core's routing rules instead of going out directly.
	RouteThroughXray bool
	XrayRoutePort    int

	// WebBackendEnable adds a loopback [[listener]] accepting the pre-FakeTLS dd
	// and plain secret forms, the only ones Telegram's WEB proxy front-end dials.
	WebBackendEnable bool
	// WebBackendPort is backend-owned, allocated and persisted on save by
	// normalizeWebBackendPort; zero means none, so no listener is emitted.
	WebBackendPort int

	// WebEnable runs mtg's WEB relay as a second process off the same file, for
	// Telegram Desktop's WEB proxy. WebHost is the name clients type; exactly one
	// of WebCoverDir and WebCoverUpstream is the site everyone else sees; the two
	// ports are loopback and backend-owned like WebBackendPort.
	WebEnable        bool
	WebHost          string
	WebCoverDir      string
	WebCoverUpstream string
	WebBindPort      int
	WebStatsPort     int
}

// webRelayConfigured says whether the relay has everything it refuses to start
// without; a half-filled form renders no [web] table and starts no relay.
func (inst Instance) webRelayConfigured() bool {
	return inst.WebEnable && inst.WebHost != "" && inst.WebBindPort > 0 && inst.WebBackendPort > 0 &&
		(inst.WebCoverDir != "") != (inst.WebCoverUpstream != "")
}

// bindTo renders the listen address mtg parses. JoinHostPort, not a bare colon:
// an IPv6 listen needs brackets, and without them mtg refuses the config and the
// sidecar never comes up — every client of that inbound offline. F215.
func (inst Instance) bindTo() string {
	listen := inst.Listen
	if listen == "" {
		listen = "0.0.0.0"
	}
	return net.JoinHostPort(listen, strconv.Itoa(inst.Port))
}

// StructuralFingerprint changes whenever a value outside the [secrets] section
// of the generated TOML changes. Such a change can only be applied by
// restarting mtg, unlike a secrets-only change, which a reload-capable mtg can
// absorb in place.
func (inst Instance) StructuralFingerprint() string {
	parts := []string{
		inst.bindTo(),
		strconv.FormatBool(inst.Debug),
		strconv.FormatBool(inst.ProxyProtocolListener),
		inst.PreferIP,
		inst.FrontingIP,
		strconv.Itoa(inst.FrontingPort),
		strconv.FormatBool(inst.FrontingProxyProtocol),
		strconv.Itoa(inst.ThrottleMaxConnections),
		strconv.FormatBool(inst.RouteThroughXray),
		strconv.Itoa(inst.XrayRoutePort),
		inst.PublicIPv4,
		inst.PublicIPv6,
		strconv.FormatBool(inst.WebBackendEnable),
		strconv.Itoa(inst.WebBackendPort),
		strconv.FormatBool(inst.WebEnable),
		inst.WebHost,
		inst.WebCoverDir,
		inst.WebCoverUpstream,
		strconv.Itoa(inst.WebBindPort),
		strconv.Itoa(inst.WebStatsPort),
	}
	return strings.Join(parts, "|")
}

// SecretsFingerprint identifies the reloadable secret config regardless of
// client order, so a reordered clients array in the stored settings does not
// read as a change. It moves whenever a client is added, removed, disabled,
// re-keyed, re-tagged, or re-limited (quota, expiry, speed, address limit) —
// all of which mtg applies in place without dropping connections.
func (inst Instance) SecretsFingerprint() string {
	pairs := make([]string, 0, len(inst.Secrets))
	for _, e := range inst.Secrets {
		pairs = append(pairs, fmt.Sprintf("%s=%s;tag=%s;q=%d;exp=%d;up=%d;down=%d;ipl=%d",
			e.Name, e.Secret, e.AdTag, e.QuotaBytes, e.ExpiresUnix, e.SpeedUpBytesPerSecond, e.SpeedDownBytesPerSecond, e.IPLimit))
	}
	slices.Sort(pairs)
	return strings.Join(pairs, "|")
}

// Traffic is a per-client traffic delta scraped from an mtg /stats endpoint. Tag
// is the owning inbound's tag and Email is the client the bytes belong to.
type Traffic struct {
	Tag   string
	Email string
	Up    int64
	Down  int64
}

// Counter keys carry their direction. The separator is a byte no email can
// contain, so splitting a key back into email and direction is unambiguous.
const (
	keySep  = "\x00"
	upKey   = keySep + "up"
	downKey = keySep + "down"
)

type managed struct {
	proc         *Process
	tag          string
	structuralFP string
	secretsFP    string
	apiPort      int
	apiToken     string
	// routed mirrors RouteThroughXray for the running instance. It lives with
	// the process because that is what is actually serving, not with whoever
	// last happened to apply it.
	routed bool
	// features is what the last successful scrape said this binary supports. It
	// is reported, never used to gate what the panel writes into the config.
	features []string
	// counter outlives the process it belongs to: mtg's counters restart at
	// zero, and only a surviving baseline can tell that apart from idle.
	counter *core.Counter
	// ring outlives it for the same reason: the lines a sidecar printed on its
	// way down are read after the restart that replaced it.
	ring *supervisor.LineRing
	// relay is the WEB relay beside this proxy, nil while the inbound wants
	// none; relayCheck is why the last start was refused, for Health to say.
	relay      *Process
	relayStats int
	relayCheck string
	// relayDeaths counts relay processes seen dead, once each, reported as
	// Restarts: the supervise job turns a grown count into a crash event.
	relayDeaths   uint32
	relayDeadSeen *Process
}

// noteRelayDeath counts a dead relay once, whoever notices it first: Health
// before the pass's reconcile, or the reconcile that replaces it.
func (cur *managed) noteRelayDeath() {
	if cur.relay != nil && !cur.relay.IsRunning() && cur.relayDeadSeen != cur.relay {
		cur.relayDeadSeen, cur.relayDeaths = cur.relay, cur.relayDeaths+1
	}
}

// Manager owns the set of running mtg processes keyed by inbound id.
type Manager struct {
	mu sync.Mutex
	// scrapeMu serialises every scrape, the periodic one and the final one a
	// stop takes. Counter.Observe assumes readings arrive in order; two
	// overlapping scrapes can invert them and re-bill a counter.
	scrapeMu sync.Mutex
	procs    map[int]*managed
	// pending is what a stopped process was billed for in its last interval,
	// handed to the next CollectTraffic. Without it those bytes were lost. F090.
	pending []Traffic
	// swept records that the one-time startup cleanup of orphaned mtg
	// processes (survivors of a previous p-ui run) has already run.
	swept bool
	// drawPort supplies the api-bind-to port. Nil means sys.FreeLoopbackPort; a
	// test substitutes a deterministic draw.
	drawPort func() (int, error)
	// rates is the panel's evaluated ladder per inbound, in the contract's bits
	// per second, folded into every render so the three writers agree (Q8).
	rates map[int]map[string]core.RateLimit
}

// apiPortDrawAttempts bounds the re-rolls when the OS keeps handing back the one
// loopback port this instance may not have.
const apiPortDrawAttempts = 8

// drawAPIPort picks the sidecar's api-bind-to port, never one of avoid — the WEB
// ports of the same mtg, whose losing bind would be silent because it is non-fatal.
func (m *Manager) drawAPIPort(avoid ...int) (int, error) {
	draw := m.drawPort
	if draw == nil {
		draw = sys.FreeLoopbackPort
	}
	for range apiPortDrawAttempts {
		port, err := draw()
		if err != nil {
			return 0, err
		}
		if !slices.Contains(avoid, port) {
			return port, nil
		}
	}
	names := make([]string, 0, len(avoid))
	for _, port := range avoid {
		names = append(names, strconv.Itoa(port))
	}
	return 0, fmt.Errorf("mtproto: no api port clear of the WEB port %s after %d attempts", strings.Join(names, ", "), apiPortDrawAttempts)
}

// webPorts are the loopback ports this instance will bind beside the api port:
// only those it actually wants, since a stored port of a disabled feature binds nothing.
func (inst Instance) webPorts() []int {
	var ports []int
	if inst.WebBackendEnable && inst.WebBackendPort > 0 {
		ports = append(ports, inst.WebBackendPort)
	}
	if inst.webRelayConfigured() {
		ports = append(ports, inst.WebBindPort, inst.WebStatsPort)
	}
	return ports
}

var (
	managerOnce sync.Once
	manager     *Manager
)

// GetManager returns the process-wide mtg manager singleton.
func GetManager() *Manager {
	managerOnce.Do(func() {
		manager = &Manager{procs: map[int]*managed{}}
	})
	return manager
}

// InstanceFromInbound derives a desired Instance from an mtproto inbound,
// building one named secret per active client. Secrets are healed on save (see
// normalizeMtprotoSecret) and by the migration, so they are read as-is here to
// keep the fingerprint stable across reconciles. Returns false when the inbound
// is not a usable mtproto inbound or has no active client secret to serve.
func InstanceFromInbound(ib *model.Inbound) (Instance, bool) {
	if ib == nil || ib.Protocol != model.MTProto {
		return Instance{}, false
	}
	var parsed instanceSettings
	if err := json.Unmarshal([]byte(ib.Settings), &parsed); err != nil {
		return Instance{}, false
	}
	inst := Instance{Id: ib.Id, Tag: ib.Tag, Listen: ib.Listen, Port: ib.Port}
	parsed.applyTo(&inst)
	inst.Secrets = make([]SecretEntry, 0, len(parsed.Clients))
	for _, c := range parsed.Clients {
		usable := UsableSecret(c.Secret)
		if !c.Enable || usable == "" || c.Email == "" {
			continue
		}
		entry := SecretEntry{Name: c.Email, Secret: usable, AdTag: UsableAdTag(c.AdTag)}
		if c.TotalGB > 0 {
			entry.QuotaBytes = c.TotalGB
		}
		if c.ExpiryTime > 0 {
			entry.ExpiresUnix = c.ExpiryTime / 1000
		}
		inst.Secrets = append(inst.Secrets, entry)
	}
	if len(inst.Secrets) == 0 {
		return Instance{}, false
	}
	return inst, true
}

// instanceSettings is the mtproto-specific half of an inbound's settings JSON.
type instanceSettings struct {
	ProxyProtocolListener bool `json:"proxyProtocolListener"`
	Debug                 bool `json:"debug"`
	DomainFronting        struct {
		IP            string `json:"ip"`
		Port          int    `json:"port"`
		ProxyProtocol bool   `json:"proxyProtocol"`
	} `json:"domainFronting"`
	PreferIP               string `json:"preferIp"`
	ThrottleMaxConnections int    `json:"throttleMaxConnections"`
	RouteThroughXray       bool   `json:"routeThroughXray"`
	RouteXrayPort          int    `json:"routeXrayPort"`
	WebBackendEnable       bool   `json:"webBackendEnable"`
	WebBackendPort         int    `json:"webBackendPort"`
	WebEnable              bool   `json:"webEnable"`
	WebHost                string `json:"webHost"`
	WebCoverDir            string `json:"webCoverDir"`
	WebCoverUpstream       string `json:"webCoverUpstream"`
	WebBindPort            int    `json:"webBindPort"`
	WebStatsPort           int    `json:"webStatsPort"`
	PublicIPv4             string `json:"publicIpv4"`
	PublicIPv6             string `json:"publicIpv6"`
	Clients                []struct {
		Email      string `json:"email"`
		Secret     string `json:"secret"`
		AdTag      string `json:"adTag"`
		Enable     bool   `json:"enable"`
		TotalGB    int64  `json:"totalGB"`
		ExpiryTime int64  `json:"expiryTime"`
	} `json:"clients"`
}

func (s instanceSettings) applyTo(inst *Instance) {
	inst.Debug = s.Debug
	inst.ProxyProtocolListener = s.ProxyProtocolListener
	inst.PreferIP = s.PreferIP
	inst.FrontingIP = s.DomainFronting.IP
	inst.FrontingPort = s.DomainFronting.Port
	inst.FrontingProxyProtocol = s.DomainFronting.ProxyProtocol
	inst.ThrottleMaxConnections = s.ThrottleMaxConnections
	inst.RouteThroughXray = s.RouteThroughXray
	inst.XrayRoutePort = s.RouteXrayPort
	inst.PublicIPv4 = strings.TrimSpace(s.PublicIPv4)
	inst.PublicIPv6 = strings.TrimSpace(s.PublicIPv6)
	// The relay dials the backend listener, so wanting the relay wants that too.
	inst.WebBackendEnable = s.WebBackendEnable || s.WebEnable
	inst.WebBackendPort = s.WebBackendPort
	inst.WebEnable = s.WebEnable
	inst.WebHost = strings.TrimSpace(s.WebHost)
	inst.WebCoverDir = strings.TrimSpace(s.WebCoverDir)
	inst.WebCoverUpstream = strings.TrimSpace(s.WebCoverUpstream)
	inst.WebBindPort = s.WebBindPort
	inst.WebStatsPort = s.WebStatsPort
}

// ApplySettings fills the non-secret half of an Instance. Both ingest paths share
// it, so they cannot drift on the fingerprint that picks restart over reload.
func (inst *Instance) ApplySettings(settings string) error {
	if settings == "" {
		return nil
	}
	var parsed instanceSettings
	if err := json.Unmarshal([]byte(settings), &parsed); err != nil {
		return err
	}
	parsed.applyTo(inst)
	return nil
}

// UsableAdTag returns a stored advertising tag only when it is well-formed.
// The save paths validate tags, but settings can arrive from raw API payloads
// or older data, and one malformed tag in a generated config makes mtg reject
// the whole file — taking every client of the inbound down with it.
func UsableAdTag(tag string) string {
	tag = strings.TrimSpace(tag)
	if !model.ValidMtprotoAdTag(tag) {
		return ""
	}
	return tag
}

// UsableSecret returns a stored client secret only in the shape the sidecar
// accepts, mirroring mtglib.Secret.Set: hex or raw-URL base64, first byte 0xee,
// then at least 16 key bytes and a non-empty hostname.
//
// A malformed one is not a per-client problem. It 400s the whole PUT /secrets,
// so no client's edit hot-applies, and it fails Config.Validate at cold start,
// so mtg refuses to run and the entire inbound goes down.
func UsableSecret(secret string) string {
	secret = strings.TrimSpace(secret)
	decoded, err := hex.DecodeString(secret)
	if err != nil {
		decoded, err = base64.RawURLEncoding.DecodeString(secret)
	}
	if err != nil || len(decoded) < 1+mtgSecretKeyLength+1 || decoded[0] != mtgFakeTLSFirstByte {
		return ""
	}
	return secret
}

// Ensure starts the mtg process for an instance, or restarts it when its
// configuration changed. A no-op when the desired process is already running.
func (m *Manager) Ensure(inst Instance) error {
	m.scrapeMu.Lock()
	defer m.scrapeMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepOrphansLocked()
	return m.ensureLocked(inst)
}

// sweepOrphansLocked kills mtg processes left running by a previous p-ui run,
// exactly once per process lifetime and before any of our own mtg are started.
// Because p-ui owns every mtg process, anything alive at this point is an orphan
// that would otherwise keep holding an inbound port with a stale secret.
func (m *Manager) sweepOrphansLocked() {
	if m.swept {
		return
	}
	m.swept = true
	if n := killStrayMtgProcesses(GetBinaryPath()); n > 0 {
		logger.Warningf("mtproto: terminated %d orphaned mtg process(es) from a previous run", n)
	}
}

// ensureAction is what ensureLocked must do to move a running mtg process to a
// desired instance: leave it alone, hot-reload only its secrets, or fully
// restart it.
type ensureAction int

const (
	ensureNoop ensureAction = iota
	ensureReload
	ensureRestart
)

// ensureActionFor decides how to apply a desired instance to the currently
// managed process. A structural change (or a dead process) forces a restart; a
// secrets-only change is a candidate for an in-place reload; identical
// fingerprints on a live process need nothing.
func ensureActionFor(running bool, curStructFP, curSecretsFP, newStructFP, newSecretsFP string) ensureAction {
	if !running || curStructFP != newStructFP {
		return ensureRestart
	}
	if curSecretsFP != newSecretsFP {
		return ensureReload
	}
	return ensureNoop
}

// SetRates stores the ladder for an inbound and reports whether it moved. The
// caller pushes only on a change: a whole-set PUT per pass would hand every
// client a fresh bucket every ten seconds.
func (m *Manager) SetRates(id int, limits map[string]core.RateLimit) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.rates == nil {
		m.rates = map[int]map[string]core.RateLimit{}
	}
	if len(limits) == 0 {
		_, had := m.rates[id]
		delete(m.rates, id)
		return had
	}
	cur := m.rates[id]
	if len(cur) == len(limits) {
		same := true
		for email, l := range limits {
			if cur[email] != l {
				same = false
				break
			}
		}
		if same {
			return false
		}
	}
	m.rates[id] = maps.Clone(limits)
	return true
}

// minSpeedBytesPerSecond restates mtglib.MinSpeedBytesPerSecond: the sidecar is
// a binary, not a dependency, and it refuses anything under 64 kbit/s.
const minSpeedBytesPerSecond = 8000

// sidecarRate converts the contract's bits per second into the sidecar's bytes,
// raised to the floor rather than refused: a refused PUT restarts the process.
func sidecarRate(bitsPerSecond int64) int64 {
	if bitsPerSecond <= 0 {
		return 0
	}
	return max(bitsPerSecond/8, minSpeedBytesPerSecond)
}

// withRatesLocked overlays the stored ladder onto a copy of the instance's
// secrets, so the render and the fingerprint see one value whichever pass built
// the instance: the supervise pass, a client edit, or the rate pass.
func (m *Manager) withRatesLocked(inst Instance) Instance {
	limits := m.rates[inst.Id]
	if len(limits) == 0 {
		return inst
	}
	secrets := slices.Clone(inst.Secrets)
	for i := range secrets {
		l := limits[secrets[i].Name]
		secrets[i].SpeedUpBytesPerSecond = sidecarRate(l.UpBps)
		secrets[i].SpeedDownBytesPerSecond = sidecarRate(l.DownBps)
	}
	inst.Secrets = secrets
	return inst
}

func (m *Manager) ensureLocked(inst Instance) error {
	inst = m.withRatesLocked(inst)
	structFP := inst.StructuralFingerprint()
	secFP := inst.SecretsFingerprint()
	counter := core.NewCounter()
	ring := supervisor.NewLineRing(supervisor.RingLines)
	if cur, ok := m.procs[inst.Id]; ok {
		switch ensureActionFor(cur.proc.IsRunning(), cur.structuralFP, cur.secretsFP, structFP, secFP) {
		case ensureNoop:
			cur.tag = inst.Tag
			cur.routed = inst.RouteThroughXray
			m.ensureRelayLocked(cur, inst)
			return nil
		case ensureReload:
			if err := writeConfig(configPathForID(inst.Id), inst, cur.apiPort, cur.apiToken); err != nil {
				return err
			}
			if applySecrets(cur.apiPort, cur.apiToken, inst) {
				cur.tag = inst.Tag
				cur.secretsFP = secFP
				logger.Infof("mtproto: applied secret update to inbound %d in place", inst.Id)
				// The relay reads the file and follows it by itself; the signal
				// makes it look now rather than on its next glance.
				cur.reloadRelay()
				m.ensureRelayLocked(cur, inst)
				return nil
			}
			logger.Warningf("mtproto: live secret update unavailable for inbound %d, restarting", inst.Id)
			fallthrough
		case ensureRestart:
			counter, ring = cur.counter, cur.ring
			m.billFinalLocked(cur)
			cur.stopRelay()
			_ = cur.proc.Stop()
			delete(m.procs, inst.Id)
		}
	}
	// The api port is drawn here and stored only in memory, so the panel-side
	// claim check cannot see it; the WEB ports are the ones it must dodge.
	apiPort, err := m.drawAPIPort(inst.webPorts()...)
	if err != nil {
		return err
	}
	apiToken, err := newAPIToken()
	if err != nil {
		return err
	}
	cfgPath := configPathForID(inst.Id)
	if err := writeConfig(cfgPath, inst, apiPort, apiToken); err != nil {
		return err
	}
	proc := newProcess(cfgPath, fmt.Sprintf("inbound %d", inst.Id), inst.Id)
	proc.keepLines(ring)
	if err := proc.Start(); err != nil {
		return err
	}
	started := &managed{
		proc:         proc,
		tag:          inst.Tag,
		structuralFP: structFP,
		secretsFP:    secFP,
		apiPort:      apiPort,
		apiToken:     apiToken,
		routed:       inst.RouteThroughXray,
		counter:      counter,
		ring:         ring,
	}
	m.procs[inst.Id] = started
	logger.Infof("mtproto: started mtg for inbound %d on %s", inst.Id, inst.bindTo())
	m.ensureRelayLocked(started, inst)
	return nil
}

// Remove stops and forgets the mtg process for an inbound id.
func (m *Manager) Remove(id int) {
	m.scrapeMu.Lock()
	defer m.scrapeMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur, ok := m.procs[id]; ok {
		m.billFinalLocked(cur)
		cur.stopRelay()
		_ = cur.proc.Stop()
		delete(m.procs, id)
		_ = os.Remove(configPathForID(id))
		logger.Infof("mtproto: stopped mtg for inbound %d", id)
	}
	delete(m.rates, id)
}

// Reconcile drives the running set toward the desired instances, at boot and
// periodically. One instance failing does not stop the others; errors are joined.
// Reconcile converges the running sidecars on desired. keep names inbounds that
// must be left running even though they are not in desired — a caller that could
// not read one must not have that read count as "stop it".
func (m *Manager) Reconcile(desired []Instance, keep ...int) error {
	m.scrapeMu.Lock()
	defer m.scrapeMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweepOrphansLocked()
	want := make(map[int]struct{}, len(desired)+len(keep))
	for _, inst := range desired {
		want[inst.Id] = struct{}{}
	}
	for _, id := range keep {
		want[id] = struct{}{}
	}
	for id, cur := range m.procs {
		if _, ok := want[id]; !ok {
			m.billFinalLocked(cur)
			cur.stopRelay()
			_ = cur.proc.Stop()
			delete(m.procs, id)
			delete(m.rates, id)
			_ = os.Remove(configPathForID(id))
		}
	}
	m.removeStaleConfigsLocked(want)
	var failures []error
	for _, inst := range desired {
		if err := m.ensureLocked(inst); err != nil {
			logger.Warningf("mtproto: reconcile failed for inbound %d: %v", inst.Id, err)
			failures = append(failures, fmt.Errorf("inbound %d: %w", inst.Id, err))
		}
	}
	return errors.Join(failures...)
}

// StopAll stops every managed mtg process. Called on panel shutdown.
func (m *Manager) StopAll() {
	m.scrapeMu.Lock()
	defer m.scrapeMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, cur := range m.procs {
		m.billFinalLocked(cur)
		cur.stopRelay()
		_ = cur.proc.Stop()
		_ = os.Remove(configPathForID(id))
		delete(m.procs, id)
	}
}

// CollectTraffic returns the per-client byte deltas since the previous scrape and
// the emails currently connected. mtg counts cumulatively; core.Counter converts.
// CollectTraffic also names the running sidecars whose /stats could not be
// read this pass: a scrape that fails silently is a client that is not billed.
func (m *Manager) CollectTraffic() (out []Traffic, online []string, failed []string) {
	m.scrapeMu.Lock()
	defer m.scrapeMu.Unlock()
	type snap struct {
		id       int
		mg       *managed
		apiPort  int
		apiToken string
		tag      string
		counter  *core.Counter
	}
	m.mu.Lock()
	// What stopped processes were billed for since the last pass goes first.
	out = m.pending
	m.pending = nil
	snaps := make([]snap, 0, len(m.procs))
	for id, cur := range m.procs {
		if cur.proc == nil || !cur.proc.IsRunning() {
			continue
		}
		snaps = append(snaps, snap{id: id, mg: cur, apiPort: cur.apiPort, apiToken: cur.apiToken, tag: cur.tag, counter: cur.counter})
	}
	m.mu.Unlock()

	for _, s := range snaps {
		stats, ok := scrapeStats(s.apiPort, s.apiToken)
		if !ok {
			failed = append(failed, s.tag)
			continue
		}
		m.mu.Lock()
		// Only while this is still the same process: a restart in between would
		// otherwise stamp the old binary's capabilities onto the new one.
		if m.procs[s.id] == s.mg {
			if len(s.mg.features) == 0 && len(stats.Features) > 0 {
				warnMissingFeatures(s.id, stats.Features)
			}
			s.mg.features = stats.Features
		}
		m.mu.Unlock()
		for email, u := range stats.Users {
			if u.Connections > 0 {
				online = append(online, email)
			}
		}
		out = append(out, billReadings(s.tag, s.counter, stats)...)
	}
	return out, online, failed
}

// requiredFeatures is what the panel drives on every sidecar: without them a
// speed, a cut or a device's recency is written and silently does nothing.
var requiredFeatures = []string{"speed-directions", "kick", "client-ip-activity"}

// warnMissingFeatures names, once per process life, what this binary lacks.
func warnMissingFeatures(id int, features []string) {
	var missing []string
	for _, want := range requiredFeatures {
		if !slices.Contains(features, want) {
			missing = append(missing, want)
		}
	}
	if len(missing) > 0 {
		logger.Warningf("mtproto: the sidecar for inbound %d lacks %s; speed and device limits will not apply until mtg-proxy is upgraded",
			id, strings.Join(missing, ", "))
	}
}

// Sessions is every connected address the running sidecars report right now,
// one per client and place, with the second that place last moved a byte. Read
// now rather than replayed from the traffic pass, so a cut a moment ago is gone.
func (m *Manager) Sessions() []core.Session {
	var out []core.Session
	for _, t := range m.runningTargets() {
		stats, ok := scrapeStats(t.port, t.token)
		if !ok {
			continue
		}
		for email, u := range stats.Users {
			for _, text := range u.ConnectedIPs {
				source, err := netip.ParseAddr(text)
				if err != nil {
					continue
				}
				s := core.Session{Email: email, Source: source}
				if at, ok := u.ConnectedIPsLastSeen[text]; ok && !at.IsZero() {
					s.LastSeenUnixMilli = at.UnixMilli()
				}
				out = append(out, s)
			}
		}
	}
	return out
}

// EnforcedRates reads back what the sidecar for an inbound holds each client
// to, in the contract's bits per second: a limit the process never took must
// not read as enforced, so this is the daemon's answer and not the table's.
func (m *Manager) EnforcedRates(id int) (map[string]core.RateLimit, error) {
	m.mu.Lock()
	cur, ok := m.procs[id]
	var port int
	var token string
	if ok {
		port, token = cur.apiPort, cur.apiToken
	}
	m.mu.Unlock()
	if !ok {
		return map[string]core.RateLimit{}, nil
	}
	stats, scraped := scrapeStats(port, token)
	if !scraped {
		return nil, fmt.Errorf("mtproto: the sidecar for inbound %d does not answer", id)
	}
	out := make(map[string]core.RateLimit, len(stats.Users))
	for email, u := range stats.Users {
		if u.SpeedLimitUp > 0 || u.SpeedLimitDown > 0 {
			out[email] = core.RateLimit{UpBps: u.SpeedLimitUp * 8, DownBps: u.SpeedLimitDown * 8}
		}
	}
	return out, nil
}

// Kick ends a client's streams from one place on every running sidecar and
// reports how many ended. A sidecar that does not know the client is nothing
// to end there, not an error: the panel kicks by email without knowing which
// inbound holds it.
func (m *Manager) Kick(email string, source netip.Addr) (int, error) {
	var (
		closed   int
		failures []error
	)
	for _, t := range m.runningTargets() {
		n, err := kick(t.port, t.token, email, source)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		closed += n
	}
	return closed, errors.Join(failures...)
}

type apiTarget struct {
	id    int
	port  int
	token string
}

// runningTargets snapshots the API address of every live sidecar under m.mu,
// so the HTTP round trips that follow hold no lock.
func (m *Manager) runningTargets() []apiTarget {
	m.mu.Lock()
	defer m.mu.Unlock()
	targets := make([]apiTarget, 0, len(m.procs))
	for id, cur := range m.procs {
		if cur.proc != nil && cur.proc.IsRunning() {
			targets = append(targets, apiTarget{id: id, port: cur.apiPort, token: cur.apiToken})
		}
	}
	slices.SortFunc(targets, func(a, b apiTarget) int { return a.id - b.id })
	return targets
}

func kick(port int, token, email string, source netip.Addr) (int, error) {
	body, err := json.Marshal(struct {
		IPs []string `json:"ips"`
	}{IPs: []string{source.Unmap().WithZone("").String()}})
	if err != nil {
		return 0, err
	}
	client := http.Client{Timeout: 3 * time.Second}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/secrets/%s/kick", port, url.PathEscape(email))
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	authorize(req, token)
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return 0, nil
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("mtproto: kick %q from %s returned %s", email, source, resp.Status)
	}
	var result struct {
		Closed int `json:"closed"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	return result.Closed, nil
}

// billReadings turns one /stats reading into per-client deltas through the
// counter that owns that process's baseline.
func billReadings(tag string, counter *core.Counter, stats mtgStats) []Traffic {
	readings := make(map[string]int64, 2*len(stats.Users))
	for email, u := range stats.Users {
		readings[email+upKey] = u.BytesIn
		readings[email+downKey] = u.BytesOut
	}
	billed := make(map[string]*Traffic)
	for key, delta := range counter.Observe(stats.StartedAt, readings) {
		email, direction, _ := strings.Cut(key, keySep)
		t, seen := billed[email]
		if !seen {
			t = &Traffic{Tag: tag, Email: email}
			billed[email] = t
		}
		if direction == "up" {
			t.Up += delta
		} else {
			t.Down += delta
		}
	}
	out := make([]Traffic, 0, len(billed))
	for _, t := range billed {
		out = append(out, *t)
	}
	return out
}

// billFinalLocked scrapes a process one last time before it is stopped and
// keeps the result for the next CollectTraffic. mtg holds its counters in
// memory only, so every byte since the previous scrape died with it. Called
// with scrapeMu and mu held, so it cannot interleave with a periodic scrape
// on the same counter. F090.
func (m *Manager) billFinalLocked(cur *managed) {
	if cur == nil || cur.counter == nil {
		return
	}
	stats, ok := scrapeStats(cur.apiPort, cur.apiToken)
	if !ok {
		return
	}
	m.pending = append(m.pending, billReadings(cur.tag, cur.counter, stats)...)
}

func (m *Manager) HasRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, cur := range m.procs {
		if cur.proc != nil && cur.proc.IsRunning() {
			return true
		}
	}
	return false
}

// ResetQuota clears the sidecar's own usage counter for a renewed client. A
// client the daemon does not know is not a failure: the panel resets by email
// without knowing which core holds it.
func (m *Manager) ResetQuota(email string) error {
	if email == "" {
		return nil
	}
	type target struct {
		port  int
		token string
	}
	m.mu.Lock()
	targets := make([]target, 0, len(m.procs))
	for _, cur := range m.procs {
		if cur.proc != nil && cur.proc.IsRunning() {
			targets = append(targets, target{cur.apiPort, cur.apiToken})
		}
	}
	m.mu.Unlock()
	var failures []error
	for _, t := range targets {
		if err := resetQuota(t.port, t.token, email); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func resetQuota(port int, token, email string) error {
	client := http.Client{Timeout: 3 * time.Second}
	endpoint := fmt.Sprintf("http://127.0.0.1:%d/secrets/%s/reset-quota", port, url.PathEscape(email))
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, endpoint, nil)
	if err != nil {
		return err
	}
	authorize(req, token)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 == 2 || resp.StatusCode == http.StatusNotFound {
		return nil
	}
	return fmt.Errorf("mtproto: reset-quota for %q returned %s", email, resp.Status)
}

// renderConfig builds the mtg-proxy TOML for an instance. Top-level keys must
// precede any [section] header in TOML, and [secrets] must be the final section
// so trailing keys are not swallowed by another table. The layout is therefore:
// top-level scalars (incl. api-bind-to and api-token), then [domain-fronting],
// [network], [throttle], any [[listener]] and the relay's [web], then
// [secret-ad-tags] for clients overriding the global advertising tag, and
// finally [secrets] with one named secret per active client.
func renderConfig(inst Instance, apiPort int, apiToken string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "bind-to = %q\n", inst.bindTo())
	if inst.Debug {
		b.WriteString("debug = true\n")
	}
	if inst.ProxyProtocolListener {
		b.WriteString("proxy-protocol-listener = true\n")
	}
	if inst.PreferIP != "" {
		fmt.Fprintf(&b, "prefer-ip = %q\n", inst.PreferIP)
	}
	fmt.Fprintf(&b, "api-bind-to = \"127.0.0.1:%d\"\n", apiPort)
	if apiToken != "" {
		fmt.Fprintf(&b, "api-token = %q\n", apiToken)
	}
	// Always on: the device cap observes addresses for every core alike, and
	// without it the sidecar reports counts with no address to decide on.
	b.WriteString("track-client-ips = true\n")
	if inst.PublicIPv4 != "" {
		fmt.Fprintf(&b, "public-ipv4 = %q\n", inst.PublicIPv4)
	}
	if inst.PublicIPv6 != "" {
		fmt.Fprintf(&b, "public-ipv6 = %q\n", inst.PublicIPv6)
	}
	if inst.FrontingIP != "" || inst.FrontingPort > 0 || inst.FrontingProxyProtocol {
		b.WriteString("\n[domain-fronting]\n")
		if inst.FrontingIP != "" {
			fmt.Fprintf(&b, "host = %q\n", inst.FrontingIP)
		}
		if inst.FrontingPort > 0 {
			fmt.Fprintf(&b, "port = %d\n", inst.FrontingPort)
		}
		if inst.FrontingProxyProtocol {
			b.WriteString("proxy-protocol = true\n")
		}
	}
	// When the inbound opts into Xray routing, mtg reaches Telegram through the
	// loopback SOCKS bridge the panel injects into the running Xray config. mtg
	// only supports SOCKS5 upstreams, which is exactly what the bridge exposes.
	if inst.RouteThroughXray && inst.XrayRoutePort > 0 {
		fmt.Fprintf(&b, "\n[network]\nproxies = [\"socks5://127.0.0.1:%d\"]\n", inst.XrayRoutePort)
	}
	if inst.ThrottleMaxConnections > 0 {
		fmt.Fprintf(&b, "\n[throttle]\nmax-connections = %d\n", inst.ThrottleMaxConnections)
	}
	// An array of tables must precede the trailing key-value tables. An older
	// mtg-proxy ignores unknown TOML, so the block needs no capability gate.
	if inst.WebBackendEnable && inst.WebBackendPort > 0 {
		fmt.Fprintf(&b, "\n[[listener]]\nbind-to = \"127.0.0.1:%d\"\naccept = [\"dd\", \"plain\"]\n", inst.WebBackendPort)
		// The relay hands the browser's address over PROXY v2; without this the
		// proxy sees every WEB user as 127.0.0.1 and the device cap counts nothing.
		if inst.webRelayConfigured() {
			b.WriteString("proxy-protocol = true\n")
		}
	}
	if inst.webRelayConfigured() {
		fmt.Fprintf(&b, "\n[web]\nenabled = true\nhost = %q\nbind-to = \"127.0.0.1:%d\"\nbackend = \"127.0.0.1:%d\"\ntoken-key = %q\n",
			inst.WebHost, inst.WebBindPort, inst.WebBackendPort, webKeyPath(inst.Id))
		if inst.WebCoverDir != "" {
			fmt.Fprintf(&b, "cover-dir = %q\n", inst.WebCoverDir)
		} else {
			fmt.Fprintf(&b, "cover-upstream = %q\n", inst.WebCoverUpstream)
		}
		if inst.WebStatsPort > 0 {
			fmt.Fprintf(&b, "stats-bind-to = \"127.0.0.1:%d\"\n", inst.WebStatsPort)
		}
	}
	// Only clients present in [secrets] may appear here: mtg rejects a config
	// whose [secret-ad-tags] names an unknown secret, so a disabled client's
	// override must vanish together with its secret.
	secrets := dedupSecrets(inst.Secrets)
	tagged := false
	for _, e := range secrets {
		if e.AdTag == "" {
			continue
		}
		if !tagged {
			b.WriteString("\n[secret-ad-tags]\n")
			tagged = true
		}
		fmt.Fprintf(&b, "%q = %q\n", e.Name, e.AdTag)
	}
	for _, e := range secrets {
		if !e.limited() {
			continue
		}
		fmt.Fprintf(&b, "\n[secret-limits.%q]\n", e.Name)
		if e.QuotaBytes > 0 {
			fmt.Fprintf(&b, "quota = %q\n", quotaString(e.QuotaBytes))
		}
		if e.ExpiresUnix > 0 {
			fmt.Fprintf(&b, "expires = %q\n", expiresString(e.ExpiresUnix))
		}
		if e.SpeedUpBytesPerSecond > 0 {
			fmt.Fprintf(&b, "speed-up = %q\n", strconv.FormatInt(e.SpeedUpBytesPerSecond, 10))
		}
		if e.SpeedDownBytesPerSecond > 0 {
			fmt.Fprintf(&b, "speed-down = %q\n", strconv.FormatInt(e.SpeedDownBytesPerSecond, 10))
		}
		if e.IPLimit > 0 {
			fmt.Fprintf(&b, "ip-limit = %d\n", e.IPLimit)
		}
	}
	b.WriteString("\n[secrets]\n")
	for _, e := range secrets {
		fmt.Fprintf(&b, "%q = %q\n", e.Name, e.Secret)
	}
	return b.String()
}

// limited says whether the client needs a [secret-limits] table at all.
func (e SecretEntry) limited() bool {
	return e.QuotaBytes > 0 || e.ExpiresUnix > 0 || e.SpeedUpBytesPerSecond > 0 || e.SpeedDownBytesPerSecond > 0 || e.IPLimit > 0
}

// dedupSecrets collapses entries sharing a name, the last winning, which is what
// secretsPayload's map already does. A repeated TOML key is a parse error. F220.
func dedupSecrets(entries []SecretEntry) []SecretEntry {
	at := make(map[string]int, len(entries))
	out := make([]SecretEntry, 0, len(entries))
	for _, e := range entries {
		if i, dup := at[e.Name]; dup {
			logger.Warning("mtproto: two clients share the name", e.Name,
				"- keeping the last; the config would otherwise repeat a TOML key and the process would not start")
			out[i] = e
			continue
		}
		at[e.Name] = len(out)
		out = append(out, e)
	}
	return out
}

func writeConfig(path string, inst Instance, apiPort int, apiToken string) error {
	if err := os.MkdirAll(configDir(), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(renderConfig(inst, apiPort, apiToken)), 0o640)
}

// statsUser is one entry of the mtg-proxy /stats users map. bytes_in is traffic
// the client sent to the proxy (upload) and bytes_out is what the proxy returned
// (download).
type statsUser struct {
	Connections int64 `json:"connections"`
	BytesIn     int64 `json:"bytes_in"`
	BytesOut    int64 `json:"bytes_out"`
	// The addresses behind the connections and when each last moved a byte,
	// present only with track-client-ips; the caps the sidecar holds, bytes/s.
	ConnectedIPs         []string             `json:"connected_ips"`
	ConnectedIPsLastSeen map[string]time.Time `json:"connected_ips_last_seen"`
	SpeedLimitUp         int64                `json:"speed_limit_up"`
	SpeedLimitDown       int64                `json:"speed_limit_down"`
}

// mtgStats is the /stats payload. StartedAt names the process incarnation, so a
// restart is billed in full; a build that omits it falls back to the backstop.
type mtgStats struct {
	StartedAt string `json:"started_at"`
	// Features is the sidecar's capability list. A binary too old to publish one
	// omits it, and this payload decodes non-strictly, so both shapes parse.
	Features []string             `json:"features"`
	Users    map[string]statsUser `json:"users"`
}

type secretPutEntry struct {
	Secret    string `json:"secret"`
	AdTag     string `json:"ad_tag,omitempty"`
	Quota     string `json:"quota,omitempty"`
	Expires   string `json:"expires,omitempty"`
	SpeedUp   string `json:"speed_up,omitempty"`
	SpeedDown string `json:"speed_down,omitempty"`
	IPLimit   int    `json:"ip_limit,omitempty"`
}

type secretsPutBody struct {
	Secrets map[string]secretPutEntry `json:"secrets"`
}

func secretsPayload(inst Instance) secretsPutBody {
	secrets := make(map[string]secretPutEntry, len(inst.Secrets))
	for _, e := range inst.Secrets {
		entry := secretPutEntry{Secret: e.Secret, AdTag: e.AdTag}
		if e.QuotaBytes > 0 {
			entry.Quota = quotaString(e.QuotaBytes)
		}
		if e.ExpiresUnix > 0 {
			entry.Expires = expiresString(e.ExpiresUnix)
		}
		if e.SpeedUpBytesPerSecond > 0 {
			entry.SpeedUp = strconv.FormatInt(e.SpeedUpBytesPerSecond, 10)
		}
		if e.SpeedDownBytesPerSecond > 0 {
			entry.SpeedDown = strconv.FormatInt(e.SpeedDownBytesPerSecond, 10)
		}
		entry.IPLimit = e.IPLimit
		secrets[e.Name] = entry
	}
	return secretsPutBody{Secrets: secrets}
}

func quotaString(bytes int64) string {
	return strconv.FormatInt(bytes, 10) + "B"
}

func expiresString(unix int64) string {
	return time.Unix(unix, 0).UTC().Format(time.RFC3339)
}

// newAPIToken mints the bearer token one mtg process and its manager share for
// the lifetime of that process. The management API can replace the whole
// secret set, so even though it only listens on loopback it must not be open
// to every local process. The token lives in the generated config (mtg reads
// it at startup only — a rewritten token would not apply until a restart,
// which is why the reload path reuses the stored one) and in the manager's
// memory, nowhere else.
func newAPIToken() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func authorize(req *http.Request, token string) {
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
}

// applySecrets pushes the desired secret set and advertising tags to a running
// mtg-proxy through its management API (PUT /secrets on the same loopback port
// that serves /stats), so a client add, removal, re-key, or ad-tag change is
// applied in place. mtg keeps every connection whose secret is unchanged and
// closes only the removed or re-keyed ones. It returns true only on a 200: an
// older binary without the endpoint (404), a refused connection, or any other
// status yields false, so the caller falls back to a full restart.
func applySecrets(port int, token string, inst Instance) bool {
	body, err := json.Marshal(secretsPayload(inst))
	if err != nil {
		return false
	}
	client := http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPut, fmt.Sprintf("http://127.0.0.1:%d/secrets", port), bytes.NewReader(body))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	authorize(req, token)
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// scrapeStats reads the mtg-proxy /stats JSON API and returns the per-user
// cumulative counters. Best-effort: an unreachable endpoint or unparseable body
// yields ok=false.
func scrapeStats(port int, token string) (mtgStats, bool) {
	client := http.Client{Timeout: 3 * time.Second}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/stats", port), nil)
	if err != nil {
		return mtgStats{}, false
	}
	authorize(req, token)
	resp, err := client.Do(req)
	if err != nil {
		return mtgStats{}, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return mtgStats{}, false
	}
	var parsed mtgStats
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return mtgStats{}, false
	}
	return parsed, true
}

// Features names the capabilities the sidecar for an inbound reported on its
// last successful scrape. Empty for a binary too old to publish the list.
func (m *Manager) Features(id int) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.procs[id]
	if !ok {
		return nil
	}
	return slices.Clone(cur.features)
}

// RoutedTags names the running instances whose egress goes out through Xray's
// loopback bridge. Their bytes are metered there under the same tag, so a
// caller billing per-inbound totals must not count them a second time.
func (m *Manager) RoutedTags() map[string]bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]bool, len(m.procs))
	for _, cur := range m.procs {
		if cur.routed && cur.tag != "" {
			out[cur.tag] = true
		}
	}
	return out
}

// Mirrors mtglib's own constants (secretFakeTLSFirstByte, SecretKeyLength). The
// sidecar is a binary, not a dependency, so the values are restated here.
const (
	mtgFakeTLSFirstByte = 0xee
	mtgSecretKeyLength  = 16
)

// removeStaleConfigsLocked deletes every generated config whose inbound is not
// wanted. Keyed on the directory and not on m.procs: the map is empty at boot,
// so a toml for an inbound deleted while the panel was down -- every one of its
// client secrets, on disk -- survived until someone noticed. F221.
func (m *Manager) removeStaleConfigsLocked(want map[int]struct{}) {
	paths, err := filepath.Glob(filepath.Join(configDir(), "mtg-*.toml"))
	if err != nil {
		return
	}
	removed := 0
	for _, path := range paths {
		var id int
		if _, err := fmt.Sscanf(filepath.Base(path), "mtg-%d.toml", &id); err != nil {
			continue
		}
		if _, ok := want[id]; ok {
			continue
		}
		if os.Remove(path) == nil {
			removed++
		}
	}
	if removed > 0 {
		logger.Infof("mtproto: removed %d config(s) for inbound(s) that no longer exist", removed)
	}
}

// Health is each sidecar as the manager sees it, ready when its stats API
// answers; a dead one carries its last line, since that is where mtg says why.
// An inbound with a relay is one instance still: a relay that is down or not
// answering makes it degraded, and Detail says which of the two it is.
func (m *Manager) Health() []core.InstanceHealth {
	type probe struct {
		id         int
		tag        string
		proc       *Process
		port       int
		token      string
		relay      *Process
		relayStats int
		relayCheck string
		restarts   uint32
	}
	m.mu.Lock()
	probes := make([]probe, 0, len(m.procs))
	for id, cur := range m.procs {
		cur.noteRelayDeath()
		probes = append(probes, probe{
			id: id, tag: cur.tag, proc: cur.proc, port: cur.apiPort, token: cur.apiToken,
			relay: cur.relay, relayStats: cur.relayStats, relayCheck: cur.relayCheck, restarts: cur.relayDeaths,
		})
	}
	m.mu.Unlock()
	slices.SortFunc(probes, func(a, b probe) int { return a.id - b.id })
	out := make([]core.InstanceHealth, 0, len(probes))
	for _, p := range probes {
		h := core.InstanceHealth{InstanceID: p.id, Tag: p.tag, Restarts: p.restarts}
		if p.proc == nil {
			h.State, h.Detail = core.HealthStopped, "never started"
			out = append(out, h)
			continue
		}
		h.PID = p.proc.PID()
		if started := p.proc.Started(); !started.IsZero() {
			h.SinceUnixMilli = started.UnixMilli()
		}
		switch {
		case p.proc.IsRunning():
			h.State = core.HealthRunning
			if _, ok := scrapeStats(p.port, p.token); ok {
				h.Ready = true
			} else {
				h.Detail = "the stats API does not answer"
			}
		case p.proc.Crashed():
			h.State, h.Detail = core.HealthFailed, p.proc.GetResult()
		default:
			h.State, h.Detail = core.HealthStopped, p.proc.GetResult()
		}
		if h.State == core.HealthRunning && p.relayCheck != "" {
			h.State, h.Ready, h.Detail = core.HealthDegraded, false, "WEB relay refused to start: "+p.relayCheck
		} else if h.State == core.HealthRunning && p.relay != nil {
			if reason := relayHealth(p.relay, p.relayStats); reason != "" {
				h.State, h.Ready, h.Detail = core.HealthDegraded, false, "WEB relay: "+reason
			}
		}
		out = append(out, h)
	}
	return out
}

// Logs is the sidecar's last n lines, kept across its restarts.
func (m *Manager) Logs(id, n int) ([]string, error) {
	m.mu.Lock()
	cur, ok := m.procs[id]
	m.mu.Unlock()
	if !ok || cur.ring == nil {
		return nil, fmt.Errorf("mtproto: no sidecar for inbound %d", id)
	}
	return cur.ring.Tail(n), nil
}
