package singbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/supervisor"
)

// DefaultApplyDelay is how long edits to a running daemon are coalesced before
// one reload lands them all.
const DefaultApplyDelay = 10 * time.Second

// Deps are the host facts the core cannot decide itself.
type Deps struct {
	// Backend runs the daemons; nil selects the exec backend.
	Backend supervisor.Backend
	// User and Group are the daemon identity under systemd; "" and -1 keep the
	// panel's own, which is what a host without the p-ui-core user gets.
	User  string
	Group int
	// Binary and StateDir override the bundled binary and the state directory.
	Binary   string
	StateDir string
	// ApplyDelay overrides DefaultApplyDelay; tests set it to zero.
	ApplyDelay time.Duration
}

// Core is the sing-box adapter: one supervised process per inbound.
type Core struct {
	deps    Deps
	backend supervisor.Backend

	mu        sync.Mutex
	instances map[int]*instance
	// pending holds deltas drained before a reload, handed out on the next
	// CollectTraffic so a reload never loses what was already moved.
	pending []core.TrafficDelta
	active  map[string]struct{}
	timer   *time.Timer
	starts  int
	// seen remembers each connection's byte total and when it last moved, so
	// last-seen advances on activity and never on a mere snapshot.
	seen map[string]seenEntry
}

type seenEntry struct{ bytes, lastSeen int64 }

type instance struct {
	inst       core.Instance
	settings   Settings
	ctl        control
	dir        string
	cfgPath    string
	configHash string
	running    bool
	dirty      bool
	epoch      string
	counter    *core.Counter
}

// New returns the core; nothing runs until the first Reconcile.
func New(deps Deps) *Core {
	if deps.Group == 0 {
		deps.Group = -1
	}
	backend := deps.Backend
	if backend == nil {
		backend = supervisor.NewExec()
	}
	return &Core{deps: deps, backend: backend, instances: map[int]*instance{}, seen: map[string]seenEntry{}}
}

func (c *Core) Kinds() []core.Kind { return Kinds() }

func (c *Core) Describe() core.Descriptor {
	return core.Descriptor{
		ID:       ID,
		TitleKey: "cores.singbox.title",
		Caps: core.Capabilities{
			// A user edit is a reload, not a restart: connections survive.
			UserHotAdd:    core.Yes(),
			PerUserStats:  core.Yes(),
			QuotaPushdown: core.No(),
			OnlineUsers:   core.Yes(),
			ShareLink:     core.Yes(),
		},
	}
}

// Preflight: the bundled binary must exist and be executable.
func (c *Core) Preflight(context.Context) error {
	info, err := os.Stat(c.binaryPath())
	if err != nil {
		return fmt.Errorf("singbox: %w", err)
	}
	if info.IsDir() || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("singbox: %s is not an executable file", c.binaryPath())
	}
	return nil
}

// ApplyDelay: an edit to a running daemon lands on the next coalesced pass.
func (c *Core) ApplyDelay(core.Kind) time.Duration {
	if c.deps.ApplyDelay > 0 {
		return c.deps.ApplyDelay
	}
	return DefaultApplyDelay
}

// Reconcile converges the daemons on desired: a new or changed instance is
// applied, a missing one stopped. A daemon not yet running starts at once;
// an edit to a running one waits for the coalesced pass.
func (c *Core) Reconcile(ctx context.Context, desired []core.Instance) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	want := map[int]struct{}{}
	var failures []error
	for _, d := range desired {
		want[d.ID] = struct{}{}
		if err := c.wantLocked(ctx, d); err != nil {
			failures = append(failures, err)
		}
	}
	for id := range c.instances {
		if _, ok := want[id]; !ok {
			failures = append(failures, c.dropLocked(ctx, id))
		}
	}
	failures = append(failures, c.sweepOrphansLocked(ctx, want))
	return errors.Join(failures...)
}

// sweepOrphansLocked stops units of this core the backend still runs for an
// inbound no longer desired: a daemon that outlived a panel which then
// deleted its inbound would otherwise serve forever.
func (c *Core) sweepOrphansLocked(ctx context.Context, want map[int]struct{}) error {
	names, err := c.backend.List(ctx)
	if err != nil {
		return nil
	}
	prefix := supervisor.UnitName(string(ID), 0)
	prefix = prefix[:len(prefix)-1]
	var failures []error
	for _, name := range names {
		id, err := strconv.Atoi(strings.TrimPrefix(name, prefix))
		if !strings.HasPrefix(name, prefix) || err != nil {
			continue
		}
		if _, ok := want[id]; ok {
			continue
		}
		if err := c.backend.Stop(ctx, name); err != nil {
			failures = append(failures, fmt.Errorf("singbox: stop orphan %s: %w", name, err))
			continue
		}
		logger.Infof("sing-box: stopped orphaned unit %s", name)
	}
	return errors.Join(failures...)
}

// wantLocked records desired state for one inbound and starts it if it is not
// running; a running one is marked dirty for the next pass.
func (c *Core) wantLocked(ctx context.Context, d core.Instance) error {
	if !d.Enable {
		return c.dropLocked(ctx, d.ID)
	}
	settings, err := parseSettings(d.Kind, d.Settings)
	if err != nil {
		return fmt.Errorf("singbox: inbound %d: %w", d.ID, err)
	}
	cur, ok := c.instances[d.ID]
	if !ok {
		cur = &instance{dir: c.instanceDir(d.ID), counter: core.NewCounter()}
		cur.cfgPath = filepath.Join(cur.dir, "config.json")
		// A daemon that outlived the panel keeps the ports and secret its file
		// names; a new one gets two free loopback ports and a fresh secret.
		if ctl, ok := readControl(cur.cfgPath); ok && c.unitRunning(ctx, cur) {
			cur.ctl = ctl
		} else if cur.ctl, err = newControl(); err != nil {
			return fmt.Errorf("singbox: inbound %d: %w", d.ID, err)
		}
		c.instances[d.ID] = cur
	}
	cur.inst, cur.settings, cur.dirty = d, settings, true
	if !cur.running {
		return c.applyLocked(ctx, cur)
	}
	c.kickLocked()
	return nil
}

func (c *Core) dropLocked(ctx context.Context, id int) error {
	cur, ok := c.instances[id]
	if !ok {
		return nil
	}
	delete(c.instances, id)
	if !cur.running {
		return nil
	}
	if err := c.backend.Stop(ctx, supervisor.UnitName(string(ID), id)); err != nil {
		return fmt.Errorf("singbox: stop inbound %d: %w", id, err)
	}
	return nil
}

// kickLocked schedules the coalesced pass; a pass already scheduled is kept.
func (c *Core) kickLocked() {
	if c.timer != nil {
		return
	}
	c.timer = time.AfterFunc(c.ApplyDelay(""), func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.timer = nil
		if err := c.settleLocked(context.Background()); err != nil {
			logger.Warning("sing-box: coalesced apply:", err)
		}
	})
}

// settleLocked applies every dirty instance now: the pass the timer takes.
func (c *Core) settleLocked(ctx context.Context) error {
	var failures []error
	ids := make([]int, 0, len(c.instances))
	for id := range c.instances {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		if cur := c.instances[id]; cur.dirty {
			failures = append(failures, c.applyLocked(ctx, cur))
		}
	}
	return errors.Join(failures...)
}

/*
applyLocked renders the config and lands it: a daemon not running is started;
a running one whose config changed has its counters drained, the file
rewritten and a reload signalled. sing-box rebuilds everything on SIGHUP and
its counters restart from zero, which is why the drain comes first.
*/
func (c *Core) applyLocked(ctx context.Context, cur *instance) error {
	cur.dirty = false
	if err := c.ensureDir(cur.dir); err != nil {
		return fmt.Errorf("singbox: inbound %d: %w", cur.inst.ID, err)
	}
	certFile, keyFile, err := c.certPair(cur.dir, cur.settings)
	if err != nil {
		return fmt.Errorf("singbox: inbound %d: %w", cur.inst.ID, err)
	}
	body, err := renderConfig(cur.inst, cur.settings, cur.ctl, certFile, keyFile)
	if err != nil {
		return fmt.Errorf("singbox: inbound %d: %w", cur.inst.ID, err)
	}
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	if cur.running && hash == cur.configHash {
		return nil
	}
	// A unit that outlived the panel runs whatever file it last loaded; if
	// that differs from what is desired now, the start below is a reload.
	stale := !cur.running && c.unitRunning(ctx, cur) && fileHash(cur.cfgPath) != hash
	if err := c.writeFile(cur.cfgPath, body); err != nil {
		return fmt.Errorf("singbox: inbound %d: %w", cur.inst.ID, err)
	}
	if err := checkConfig(ctx, c.binaryPath(), cur.cfgPath); err != nil {
		return fmt.Errorf("singbox: inbound %d: %w", cur.inst.ID, err)
	}
	if cur.running {
		c.drainLocked(ctx, cur)
		before, _ := readStartedAt(ctx, cur.ctl.apiPort, cur.ctl.secret)
		err := c.backend.Reload(ctx, supervisor.UnitName(string(ID), cur.inst.ID))
		if err == nil {
			cur.configHash, cur.epoch = hash, newEpoch()
			return waitReloaded(ctx, cur, before)
		}
		if !errors.Is(err, supervisor.ErrNotRunning) {
			return fmt.Errorf("singbox: inbound %d: reload: %w", cur.inst.ID, err)
		}
	}
	if err := c.backend.Ensure(ctx, c.spec(cur.inst, cur.dir, cur.cfgPath)); err != nil {
		return fmt.Errorf("singbox: inbound %d: %w", cur.inst.ID, err)
	}
	c.starts++
	cur.running, cur.configHash, cur.epoch = true, hash, newEpoch()
	logger.Infof("sing-box: inbound %d (%s) %s on %s:%d", cur.inst.ID, cur.inst.Tag, cur.inst.Kind, listenAddress(cur.inst.Listen), cur.inst.Port)
	if stale {
		before, _ := readStartedAt(ctx, cur.ctl.apiPort, cur.ctl.secret)
		if err := c.backend.Reload(ctx, supervisor.UnitName(string(ID), cur.inst.ID)); err == nil {
			return waitReloaded(ctx, cur, before)
		}
	}
	return waitControl(ctx, cur)
}

// The daemon calls, as variables so a test can overlay what no client can make
// a QUIC daemon count — or close — on its own.
var (
	readUserStats   = queryUserStats
	readConnections = listConnections
	readStartedAt   = startedAt
	closeConn       = closeConnection
)

// waitControl blocks until the daemon answers a stats query: Ensure returns
// when the process exists, and a reload closes the servers before reopening.
func waitControl(ctx context.Context, cur *instance) error {
	deadline := time.Now().Add(15 * time.Second)
	for {
		_, err := readUserStats(ctx, cur.ctl.statsPort)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("singbox: inbound %d: the daemon did not open its stats port: %w", cur.inst.ID, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// waitReloaded blocks until the daemon reports a new start time, then until it
// answers stats: a SIGHUP is asynchronous and the old instance answers meanwhile.
func waitReloaded(ctx context.Context, cur *instance, before int64) error {
	deadline := time.Now().Add(15 * time.Second)
	for {
		now, err := readStartedAt(ctx, cur.ctl.apiPort, cur.ctl.secret)
		if err == nil && now != before {
			return waitControl(ctx, cur)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("singbox: inbound %d: the daemon did not reload within 15s", cur.inst.ID)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// unitRunning asks the backend, which under systemd remembers a unit the panel
// itself has not started this process.
func (c *Core) unitRunning(ctx context.Context, cur *instance) bool {
	st, err := c.backend.Status(ctx, supervisor.UnitName(string(ID), cur.inst.ID))
	return err == nil && st.State == supervisor.StateRunning
}

func fileHash(path string) string {
	body, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func newEpoch() string { return strconv.FormatInt(time.Now().UnixNano(), 36) }

// drainLocked bills what the daemon counted so far, so the reload that
// zeroes its counters costs nothing.
func (c *Core) drainLocked(ctx context.Context, cur *instance) {
	readings, err := readUserStats(ctx, cur.ctl.statsPort)
	if err != nil {
		logger.Warning("sing-box: drain before reload:", err)
		return
	}
	c.pending = append(c.pending, c.deltasLocked(cur, readings)...)
}

// deltasLocked feeds one daemon's cumulative counters through its engine and
// folds the result per user.
func (c *Core) deltasLocked(cur *instance, readings map[string]int64) []core.TrafficDelta {
	keyed := make(map[string]int64, len(readings))
	for name, value := range readings {
		if email, dir, ok := splitStatName(name); ok {
			keyed[email+"|"+dir] = value
		}
	}
	deltas := cur.counter.Observe(cur.epoch, keyed)
	byEmail := map[string]*core.TrafficDelta{}
	for key, d := range deltas {
		email, dir, _ := splitKey(key)
		agg, ok := byEmail[email]
		if !ok {
			agg = &core.TrafficDelta{Email: email, Tag: cur.inst.Tag}
			byEmail[email] = agg
		}
		if dir == "uplink" {
			agg.Up += d
		} else {
			agg.Down += d
		}
	}
	emails := make([]string, 0, len(byEmail))
	for email := range byEmail {
		emails = append(emails, email)
	}
	sort.Strings(emails)
	out := make([]core.TrafficDelta, 0, len(emails))
	for _, email := range emails {
		out = append(out, *byEmail[email])
	}
	return out
}

// splitKey reads "<email>|<dir>"; the email never carries a pipe.
func splitKey(key string) (email, dir string, ok bool) {
	for i := len(key) - 1; i >= 0; i-- {
		if key[i] == '|' {
			return key[:i], key[i+1:], true
		}
	}
	return key, "", false
}

// OutlivesPanel: under systemd the units keep serving through a panel restart
// and the next Reconcile adopts them; an operator's StopAll still stops them.
func (c *Core) OutlivesPanel() bool { return c.backend.Name() == string(supervisor.ModeSystemd) }

func (c *Core) StopAll(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.timer != nil {
		c.timer.Stop()
		c.timer = nil
	}
	var failures []error
	for id := range c.instances {
		failures = append(failures, c.dropLocked(ctx, id))
	}
	return errors.Join(failures...)
}

// PlanChange: a listener or TLS field restarts; a user edit is a reload.
func (c *Core) PlanChange(before, after core.Instance) core.Action {
	switch {
	case before.Enable != after.Enable, before.Port != after.Port, before.Listen != after.Listen, before.Kind != after.Kind:
		return core.ActionRestart
	case before.Settings != after.Settings:
		return core.ActionRestart
	case usersFingerprint(before.Users) != usersFingerprint(after.Users):
		return core.ActionHotApply
	}
	return core.ActionNoop
}

func usersFingerprint(users []core.User) string {
	parts := make([]string, 0, len(users))
	for _, u := range users {
		parts = append(parts, fmt.Sprintf("%s|%s|%s|%t", u.Email, core.CredString(u.Credentials, core.CredUUID), core.CredString(u.Credentials, core.CredPassword), u.Enable))
	}
	sort.Strings(parts)
	return fmt.Sprint(parts)
}

func (c *Core) ApplyInstance(ctx context.Context, inst core.Instance) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.wantLocked(ctx, inst)
}

func (c *Core) DropInstance(ctx context.Context, inst core.Instance) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dropLocked(ctx, inst.ID)
}

func (c *Core) AddUser(ctx context.Context, inst core.Instance, user core.User) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cur, ok := c.instances[inst.ID]
	if !ok {
		return fmt.Errorf("singbox: inbound %d is not running", inst.ID)
	}
	cur.inst.Users = append(withoutEmail(cur.inst.Users, user.Email), user)
	cur.dirty = true
	c.kickLocked()
	return nil
}

func (c *Core) RemoveUser(ctx context.Context, inst core.Instance, email string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	cur, ok := c.instances[inst.ID]
	if !ok {
		return nil
	}
	cur.inst.Users = withoutEmail(cur.inst.Users, email)
	cur.dirty = true
	c.kickLocked()
	return nil
}

func withoutEmail(users []core.User, email string) []core.User {
	out := make([]core.User, 0, len(users))
	for _, u := range users {
		if u.Email != email {
			out = append(out, u)
		}
	}
	return out
}

// CollectTraffic hands out what was drained before reloads, then reads every
// running daemon's counters through its own engine. The daemons are queried
// outside the lock, so a hung one stalls no edit.
func (c *Core) CollectTraffic(ctx context.Context) ([]core.TrafficDelta, error) {
	type target struct {
		id   int
		port int
	}
	c.mu.Lock()
	targets := make([]target, 0, len(c.instances))
	for id, cur := range c.instances {
		if cur.running {
			targets = append(targets, target{id: id, port: cur.ctl.statsPort})
		}
	}
	c.mu.Unlock()
	sort.Slice(targets, func(i, j int) bool { return targets[i].id < targets[j].id })
	var failures []error
	answers := make(map[int]map[string]int64, len(targets))
	for _, t := range targets {
		readings, err := readUserStats(ctx, t.port)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		answers[t.id] = readings
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	out := append([]core.TrafficDelta(nil), c.pending...)
	c.pending = nil
	for _, t := range targets {
		cur, ok := c.instances[t.id]
		readings, answered := answers[t.id]
		if !ok || !answered || cur.ctl.statsPort != t.port {
			continue
		}
		out = append(out, c.deltasLocked(cur, readings)...)
	}
	c.active = make(map[string]struct{}, len(out))
	for _, d := range out {
		c.active[d.Email] = struct{}{}
	}
	return out, errors.Join(failures...)
}

func (c *Core) OnlineEmails(ctx context.Context) ([]string, error) {
	sessions, err := c.Sessions(ctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	seen := map[string]struct{}{}
	for email := range c.active {
		seen[email] = struct{}{}
	}
	for _, s := range sessions {
		seen[s.Email] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for email := range seen {
		out = append(out, email)
	}
	sort.Strings(out)
	return out, err
}

// Sessions is a snapshot of every daemon's live connections. Last-seen is when a
// connection last moved bytes between snapshots, starting from when it opened.
func (c *Core) Sessions(ctx context.Context) ([]core.Session, error) {
	c.mu.Lock()
	type target struct{ ctl control }
	targets := make([]target, 0, len(c.instances))
	for _, cur := range c.instances {
		if cur.running {
			targets = append(targets, target{ctl: cur.ctl})
		}
	}
	c.mu.Unlock()
	var out []core.Session
	var failures []error
	var conns []liveConnection
	for _, t := range targets {
		list, err := readConnections(ctx, t.ctl.apiPort, t.ctl.secret)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		conns = append(conns, list...)
	}
	live := map[string]seenEntry{}
	now := time.Now().UnixMilli()
	c.mu.Lock()
	defer c.mu.Unlock()
	{
		for _, conn := range conns {
			key := conn.id
			if key == "" {
				key = conn.user + "|" + conn.source.String() + "|" + strconv.FormatInt(conn.createdAt, 10)
			}
			entry, known := c.seen[key]
			switch {
			case !known:
				entry = seenEntry{bytes: conn.bytes, lastSeen: conn.createdAt}
			case conn.bytes != entry.bytes:
				entry = seenEntry{bytes: conn.bytes, lastSeen: now}
			}
			live[key] = entry
			out = append(out, core.Session{Email: conn.user, Source: conn.source, LastSeenUnixMilli: entry.lastSeen, StartedUnixMilli: conn.createdAt})
		}
	}
	c.seen = live
	return out, errors.Join(failures...)
}

/*
CutSessions closes one client's live connections from one source, by the ids the
connection stream names them with, so the device over its cap is dropped and the
ones inside it stay up. sing-box admits at no login of its own, so the panel
backs this with the firewall; a cut alone would see the device straight back.
*/
func (c *Core) CutSessions(ctx context.Context, email string, source netip.Addr) (int, error) {
	c.mu.Lock()
	targets := make([]control, 0, len(c.instances))
	for _, cur := range c.instances {
		if cur.running {
			targets = append(targets, cur.ctl)
		}
	}
	c.mu.Unlock()
	cut := 0
	var failures []error
	for _, ctl := range targets {
		list, err := readConnections(ctx, ctl.apiPort, ctl.secret)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		for _, conn := range list {
			if conn.id == "" || conn.user != email || conn.source != source {
				continue
			}
			if err := closeConn(ctx, ctl.apiPort, ctl.secret, conn.id); err != nil {
				failures = append(failures, err)
				continue
			}
			cut++
		}
	}
	return cut, errors.Join(failures...)
}
