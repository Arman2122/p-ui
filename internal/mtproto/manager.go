package mtproto

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/logger"
)

// SecretEntry is one named FakeTLS secret served by an mtg-multi process. Name is
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
}

// Instance is the desired runtime configuration of one mtproto inbound. A single
// mtg-multi process serves every active client's secret through the [secrets]
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
}

func (inst Instance) bindTo() string {
	listen := inst.Listen
	if listen == "" {
		listen = "0.0.0.0"
	}
	return fmt.Sprintf("%s:%d", listen, inst.Port)
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
	}
	return strings.Join(parts, "|")
}

// SecretsFingerprint identifies the reloadable secret config regardless of
// client order, so a reordered clients array in the stored settings does not
// read as a change. It moves whenever a client is added, removed, disabled,
// re-keyed, re-tagged, or re-limited (quota/expiry) — all of which mtg applies
// in place without dropping connections.
func (inst Instance) SecretsFingerprint() string {
	pairs := make([]string, 0, len(inst.Secrets))
	for _, e := range inst.Secrets {
		pairs = append(pairs, fmt.Sprintf("%s=%s;tag=%s;q=%d;exp=%d", e.Name, e.Secret, e.AdTag, e.QuotaBytes, e.ExpiresUnix))
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
	// counter outlives the process it belongs to: mtg's counters restart at
	// zero, and only a surviving baseline can tell that apart from idle.
	counter *core.Counter
}

// Manager owns the set of running mtg processes keyed by inbound id.
type Manager struct {
	mu sync.Mutex
	// scrapeMu serialises CollectTraffic. Counter.Observe assumes readings arrive
	// in order; two overlapping scrapes can invert them and re-bill a counter.
	scrapeMu sync.Mutex
	procs    map[int]*managed
	// swept records that the one-time startup cleanup of orphaned mtg
	// processes (survivors of a previous p-ui run) has already run.
	swept bool
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
		if !c.Enable || c.Secret == "" || c.Email == "" {
			continue
		}
		entry := SecretEntry{Name: c.Email, Secret: c.Secret, AdTag: UsableAdTag(c.AdTag)}
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

// Ensure starts the mtg process for an instance, or restarts it when its
// configuration changed. A no-op when the desired process is already running.
func (m *Manager) Ensure(inst Instance) error {
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

func (m *Manager) ensureLocked(inst Instance) error {
	structFP := inst.StructuralFingerprint()
	secFP := inst.SecretsFingerprint()
	counter := core.NewCounter()
	if cur, ok := m.procs[inst.Id]; ok {
		switch ensureActionFor(cur.proc.IsRunning(), cur.structuralFP, cur.secretsFP, structFP, secFP) {
		case ensureNoop:
			cur.tag = inst.Tag
			cur.routed = inst.RouteThroughXray
			return nil
		case ensureReload:
			if err := writeConfig(configPathForID(inst.Id), inst, cur.apiPort, cur.apiToken); err != nil {
				return err
			}
			if applySecrets(cur.apiPort, cur.apiToken, inst) {
				cur.tag = inst.Tag
				cur.secretsFP = secFP
				logger.Infof("mtproto: applied secret update to inbound %d in place", inst.Id)
				return nil
			}
			logger.Warningf("mtproto: live secret update unavailable for inbound %d, restarting", inst.Id)
			fallthrough
		case ensureRestart:
			counter = cur.counter
			_ = cur.proc.Stop()
			delete(m.procs, inst.Id)
		}
	}
	apiPort, err := FreeLocalPort()
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
	if err := proc.Start(); err != nil {
		return err
	}
	m.procs[inst.Id] = &managed{
		proc:         proc,
		tag:          inst.Tag,
		structuralFP: structFP,
		secretsFP:    secFP,
		apiPort:      apiPort,
		apiToken:     apiToken,
		routed:       inst.RouteThroughXray,
		counter:      counter,
	}
	logger.Infof("mtproto: started mtg for inbound %d on %s", inst.Id, inst.bindTo())
	return nil
}

// Remove stops and forgets the mtg process for an inbound id.
func (m *Manager) Remove(id int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cur, ok := m.procs[id]; ok {
		_ = cur.proc.Stop()
		delete(m.procs, id)
		_ = os.Remove(configPathForID(id))
		logger.Infof("mtproto: stopped mtg for inbound %d", id)
	}
}

// Reconcile drives the running set toward the desired instances, at boot and
// periodically. One instance failing does not stop the others; errors are joined.
// Reconcile converges the running sidecars on desired. keep names inbounds that
// must be left running even though they are not in desired — a caller that could
// not read one must not have that read count as "stop it".
func (m *Manager) Reconcile(desired []Instance, keep ...int) error {
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
			_ = cur.proc.Stop()
			delete(m.procs, id)
			_ = os.Remove(configPathForID(id))
		}
	}
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
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, cur := range m.procs {
		_ = cur.proc.Stop()
		_ = os.Remove(configPathForID(id))
		delete(m.procs, id)
	}
}

// CollectTraffic returns the per-client byte deltas since the previous scrape and
// the emails currently connected. mtg counts cumulatively; core.Counter converts.
func (m *Manager) CollectTraffic() ([]Traffic, []string) {
	m.scrapeMu.Lock()
	defer m.scrapeMu.Unlock()
	type snap struct {
		apiPort  int
		apiToken string
		tag      string
		counter  *core.Counter
	}
	m.mu.Lock()
	snaps := make([]snap, 0, len(m.procs))
	for _, cur := range m.procs {
		if cur.proc == nil || !cur.proc.IsRunning() {
			continue
		}
		snaps = append(snaps, snap{apiPort: cur.apiPort, apiToken: cur.apiToken, tag: cur.tag, counter: cur.counter})
	}
	m.mu.Unlock()

	var out []Traffic
	var online []string
	for _, s := range snaps {
		stats, ok := scrapeStats(s.apiPort, s.apiToken)
		if !ok {
			continue
		}
		readings := make(map[string]int64, 2*len(stats.Users))
		for email, u := range stats.Users {
			readings[email+upKey] = u.BytesIn
			readings[email+downKey] = u.BytesOut
			if u.Connections > 0 {
				online = append(online, email)
			}
		}
		billed := make(map[string]*Traffic)
		for key, delta := range s.counter.Observe(stats.StartedAt, readings) {
			email, direction, _ := strings.Cut(key, keySep)
			t, seen := billed[email]
			if !seen {
				t = &Traffic{Tag: s.tag, Email: email}
				billed[email] = t
			}
			if direction == "up" {
				t.Up += delta
			} else {
				t.Down += delta
			}
		}
		for _, t := range billed {
			out = append(out, *t)
		}
	}
	return out, online
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

// FreeLocalPort asks the OS for an unused loopback TCP port. It is used both
// for mtg's /stats API endpoint and to allocate the per-inbound SOCKS egress
// bridge port persisted into mtproto inbound settings.
func FreeLocalPort() (int, error) {
	l, err := (&net.ListenConfig{}).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// renderConfig builds the mtg-multi TOML for an instance. Top-level keys must
// precede any [section] header in TOML, and [secrets] must be the final section
// so trailing keys are not swallowed by another table. The layout is therefore:
// top-level scalars (incl. api-bind-to and api-token), then [domain-fronting],
// [network] and [throttle], then [secret-ad-tags] for clients overriding the
// global advertising tag, and finally [secrets] with one named secret per
// active client.
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
	// Only clients present in [secrets] may appear here: mtg rejects a config
	// whose [secret-ad-tags] names an unknown secret, so a disabled client's
	// override must vanish together with its secret.
	tagged := false
	for _, e := range inst.Secrets {
		if e.AdTag == "" {
			continue
		}
		if !tagged {
			b.WriteString("\n[secret-ad-tags]\n")
			tagged = true
		}
		fmt.Fprintf(&b, "%q = %q\n", e.Name, e.AdTag)
	}
	for _, e := range inst.Secrets {
		if e.QuotaBytes <= 0 && e.ExpiresUnix <= 0 {
			continue
		}
		fmt.Fprintf(&b, "\n[secret-limits.%q]\n", e.Name)
		if e.QuotaBytes > 0 {
			fmt.Fprintf(&b, "quota = %q\n", quotaString(e.QuotaBytes))
		}
		if e.ExpiresUnix > 0 {
			fmt.Fprintf(&b, "expires = %q\n", expiresString(e.ExpiresUnix))
		}
	}
	b.WriteString("\n[secrets]\n")
	for _, e := range inst.Secrets {
		fmt.Fprintf(&b, "%q = %q\n", e.Name, e.Secret)
	}
	return b.String()
}

func writeConfig(path string, inst Instance, apiPort int, apiToken string) error {
	if err := os.MkdirAll(configDir(), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(renderConfig(inst, apiPort, apiToken)), 0o640)
}

// statsUser is one entry of the mtg-multi /stats users map. bytes_in is traffic
// the client sent to the proxy (upload) and bytes_out is what the proxy returned
// (download).
type statsUser struct {
	Connections int64 `json:"connections"`
	BytesIn     int64 `json:"bytes_in"`
	BytesOut    int64 `json:"bytes_out"`
}

// mtgStats is the /stats payload. StartedAt names the process incarnation, so a
// restart is billed in full; a build that omits it falls back to the backstop.
type mtgStats struct {
	StartedAt string               `json:"started_at"`
	Users     map[string]statsUser `json:"users"`
}

type secretPutEntry struct {
	Secret  string `json:"secret"`
	AdTag   string `json:"ad_tag,omitempty"`
	Quota   string `json:"quota,omitempty"`
	Expires string `json:"expires,omitempty"`
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
// mtg-multi through its management API (PUT /secrets on the same loopback port
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

// scrapeStats reads the mtg-multi /stats JSON API and returns the per-user
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
