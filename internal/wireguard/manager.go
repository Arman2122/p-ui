// Package wireguard manages kernel WireGuard devices, one per inbound: the link,
// its addresses, its peer routes and the per-peer counters the panel bills from.
package wireguard

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/logger"
)

// Traffic is one client's byte delta over one collection interval, never a
// cumulative total.
type Traffic struct {
	Tag   string
	Email string
	Up    int64
	Down  int64
}

// Counter keys carry their direction. The separator is a byte no base64 key
// contains, so splitting a key back apart is unambiguous.
const (
	keySep  = "\x00"
	upKey   = keySep + "up"
	downKey = keySep + "down"
)

// handshakeWindow is how recent a peer's last handshake must be for its client to
// count as online. WireGuard rekeys every two minutes while traffic is flowing.
const handshakeWindow = 3 * time.Minute

// managed is the panel-side state of one device. It deliberately caches nothing
// about the kernel: Ensure reads the device every time, so drift is repaired.
type managed struct {
	tag string
	// generation rises whenever the panel creates the link. Paired with the
	// ifindex it is the counter epoch, so a recreated device is billed from zero.
	generation int
	// counter outlives every link this inbound ever has: a fresh one would swallow
	// the first reading after a recreate instead of billing it.
	counter *core.Counter
	byEmail map[string]wgtypes.Key
	byKey   map[wgtypes.Key]string
	// pending holds deltas banked outside a scrape — a peer's final reading, taken
	// before a write the kernel answers by zeroing its counters.
	pending map[string]*Traffic
}

// reindex rebuilds the email/public-key mapping. The kernel counts per key and
// the panel bills per email, so a stale entry bills the wrong client.
//
// held are the peers the device still carries because a write was refused; they
// keep their owner rather than becoming keys nothing can be billed to.
func (rec *managed) reindex(peers []Peer, held []wgtypes.Peer) {
	byEmail := make(map[string]wgtypes.Key, len(peers))
	byKey := make(map[wgtypes.Key]string, len(peers))
	for _, p := range peers {
		key, err := parseKey(p.PublicKey)
		if err != nil || p.Email == "" || key == (wgtypes.Key{}) {
			continue
		}
		// First claimant wins, exactly as desiredPeers serves it: the later one
		// has no peer, so billing it the key's bytes would bill the wrong client.
		if _, claimed := byKey[key]; claimed {
			continue
		}
		byEmail[p.Email] = key
		byKey[key] = p.Email
	}
	for _, p := range held {
		email, known := rec.byKey[p.PublicKey]
		if !known {
			continue
		}
		if _, claimed := byKey[p.PublicKey]; claimed {
			continue
		}
		byKey[p.PublicKey] = email
		if _, mapped := byEmail[email]; !mapped {
			byEmail[email] = p.PublicKey
		}
	}
	rec.byEmail, rec.byKey = byEmail, byKey
}

func (rec *managed) track(email string, key wgtypes.Key) {
	if email == "" {
		return
	}
	if old, ok := rec.byEmail[email]; ok && old != key {
		delete(rec.byKey, old)
	}
	rec.byEmail[email] = key
	rec.byKey[key] = email
}

func (rec *managed) forget(email string, key wgtypes.Key) {
	delete(rec.byEmail, email)
	delete(rec.byKey, key)
	rec.counter.Forget(key.String() + upKey)
	rec.counter.Forget(key.String() + downKey)
}

// forgetRemoved drops the baselines of the peers a push has just revoked. The
// kernel gives a re-added peer fresh counters, so a kept baseline under-bills it.
func (rec *managed) forgetRemoved(changes []wgtypes.PeerConfig) {
	for _, c := range changes {
		if !c.Remove {
			continue
		}
		rec.counter.Forget(c.PublicKey.String() + upKey)
		rec.counter.Forget(c.PublicKey.String() + downKey)
	}
}

// attribute folds one Observe's deltas into per-client totals. A key no client
// owns is a peer added outside the panel: its bytes are dropped, never guessed.
func (rec *managed) attribute(deltas map[string]int64, into map[string]*Traffic) {
	for key, delta := range deltas {
		raw, direction, _ := strings.Cut(key, keySep)
		parsed, err := wgtypes.ParseKey(raw)
		if err != nil {
			continue
		}
		email, known := rec.byKey[parsed]
		if !known {
			continue
		}
		acc, seen := into[email]
		if !seen {
			acc = &Traffic{Tag: rec.tag, Email: email}
			into[email] = acc
		}
		if direction == "up" {
			acc.Up += delta
		} else {
			acc.Down += delta
		}
	}
}

// takePending removes the banked deltas so they are handed out exactly once;
// replaying them would double every drained interval.
func (rec *managed) takePending() map[string]*Traffic {
	out := rec.pending
	rec.pending = nil
	if out == nil {
		return map[string]*Traffic{}
	}
	return out
}

// deviceReadings turns one snapshot into the cumulative readings the counter
// takes and the epoch they belong to. Both halves come from the same snapshot,
// or a recreate lands in two epochs and the bytes between them are billed twice.
func deviceReadings(snap Snapshot, generation int) (map[string]int64, string) {
	readings := make(map[string]int64, 2*len(snap.Device.Peers))
	for _, peer := range snap.Device.Peers {
		pub := peer.PublicKey.String()
		readings[pub+upKey] = peer.ReceiveBytes
		readings[pub+downKey] = peer.TransmitBytes
	}
	return readings, strconv.Itoa(snap.Link.Index) + ":" + strconv.Itoa(generation)
}

// drainLocked banks every peer's reading before a write destroys some of them:
// the kernel zeroes a removed peer's counters, so an unbanked interval is lost.
//
// This is not the pending-removal map the design rejected — nothing here defers
// a write. Unprimed there is no baseline, so observing would only prime early.
func (m *Manager) drainLocked(rec *managed, snap Snapshot) {
	if !rec.counter.Primed() {
		return
	}
	readings, epoch := deviceReadings(snap, rec.generation)
	if rec.pending == nil {
		rec.pending = make(map[string]*Traffic)
	}
	rec.attribute(rec.counter.Observe(epoch, readings), rec.pending)
}

// Manager owns the kernel devices the panel serves, keyed by inbound id.
type Manager struct {
	mu    sync.Mutex
	plane Plane
	// prefix is this manager's device namespace. Two managers over one host must
	// not share it, or their id spaces collide on a single device.
	prefix string
	// scrapeMu serialises CollectTraffic. Counter.Observe assumes readings arrive
	// in order; two overlapping scrapes can invert them and re-bill a counter.
	scrapeMu sync.Mutex
	devices  map[int]*managed
	warnOnce sync.Once
}

// Name is the device this manager gives an id. It is the contract a caller
// routes by: whatever this returns is what Ensure creates.
func (m *Manager) Name(id int) string { return nameIn(m.prefix, id) }

// owned reports whether a device is in this manager's namespace, so a sweep
// never reaches across into the other's.
func (m *Manager) owned(name string) (int, bool) { return ownedIDIn(m.prefix, name) }

// NewManager returns a manager over one network stack in the inbound namespace.
// It is the single injection seam: production passes the host's, tests a fake.
func NewManager(p Plane) *Manager { return NewNamedManager(p, interfacePrefix) }

// NewNamedManager returns a manager owning its own device namespace. Each sweep
// is confined to that prefix, so two managers over one host ignore each other.
func NewNamedManager(p Plane, prefix string) *Manager {
	return &Manager{plane: p, prefix: prefix, devices: map[int]*managed{}}
}

var (
	managerOnce sync.Once
	manager     *Manager
	uplinkOnce  sync.Once
	uplinkMgr   *Manager
)

// GetManager returns the process-wide manager over the host network stack.
func GetManager() *Manager {
	managerOnce.Do(func() { manager = NewManager(hostPlane()) })
	return manager
}

// GetUplinkManager returns the process-wide manager for dialled uplinks. A
// separate manager, because an uplink id and an inbound id mean different things.
func GetUplinkManager() *Manager {
	uplinkOnce.Do(func() { uplinkMgr = NewNamedManager(hostPlane(), UplinkPrefix) })
	return uplinkMgr
}

// record returns this inbound's state, creating it on first sight. The counter is
// made here once and never replaced, or a link recreate stops being observable.
func (m *Manager) record(id int) *managed {
	rec, ok := m.devices[id]
	if !ok {
		rec = &managed{
			counter: core.NewCounter(),
			byEmail: map[string]wgtypes.Key{},
			byKey:   map[wgtypes.Key]string{},
		}
		m.devices[id] = rec
	}
	return rec
}

// configure is the only place a wgtypes.Config is built. ReplacePeers stays false
// or a single-client push wipes every other peer on the inbound.
func (m *Manager) configure(ctx context.Context, name string, dev deviceDelta, peers []wgtypes.PeerConfig) error {
	cfg := wgtypes.Config{
		PrivateKey:   dev.PrivateKey,
		ListenPort:   dev.ListenPort,
		FirewallMark: dev.FirewallMark,
		ReplacePeers: false,
		Peers:        peers,
	}
	return m.note(m.plane.Configure(ctx, name, cfg))
}

// Ensure converges one inbound's device on desired state. It snapshots and diffs
// on every call, so a change made outside the panel is repaired by the next one.
func (m *Manager) Ensure(ctx context.Context, inst Instance) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ensureLocked(ctx, inst)
}

func (m *Manager) ensureLocked(ctx context.Context, inst Instance) error {
	name := m.Name(inst.ID)
	key, err := parseKey(inst.PrivateKey)
	if err != nil {
		return fmt.Errorf("wireguard: inbound %d has an unusable private key: %w", inst.ID, err)
	}
	addrs, err := parsePrefixes(inst.Address)
	if err != nil {
		return err
	}
	// A client the kernel cannot hold costs that client alone. Abandoning the pass
	// would leave every revoked peer in place, spending against a spent quota.
	peers, rejected := desiredPeers(inst.Peers)

	snap, created, err := m.ensureDeviceLocked(ctx, inst, key)
	if err != nil {
		return errors.Join(rejected, err)
	}

	rec := m.record(inst.ID)
	rec.tag = inst.Tag
	if created {
		rec.generation++
	}

	failures := []error{rejected}
	changes := diffPeers(snap.Device.Peers, peers, created)
	// Banked while the old index still names the owners, because the removals in
	// this pass are how a revoked or depleted client leaves the device.
	if slices.ContainsFunc(changes, func(c wgtypes.PeerConfig) bool { return c.Remove }) {
		m.drainLocked(rec, snap)
	}
	var cfgErr error
	if len(changes) > 0 {
		if cfgErr = m.configure(ctx, name, deviceDelta{}, changes); cfgErr != nil {
			failures = append(failures, cfgErr)
		} else {
			rec.forgetRemoved(changes)
		}
	}
	// The index narrows only once the kernel has agreed; a refused pass leaves
	// every peer it still holds attributed to the client who owns it.
	held := []wgtypes.Peer(nil)
	if cfgErr != nil {
		held = snap.Device.Peers
	}
	rec.reindex(inst.Peers, held)
	dropped, addrFailures := m.syncAddrs(ctx, name, snap.Addrs, addrs)
	failures = append(failures, addrFailures...)
	failures = append(failures, m.syncRoutes(ctx, name, snap.Routes, desiredRoutes(addrs, peers), addrs, dropped)...)
	return errors.Join(failures...)
}

// ensureDeviceLocked brings the link up and writes the device scalars, returning
// the snapshot the peer diff is computed from and whether the link is new.
func (m *Manager) ensureDeviceLocked(ctx context.Context, inst Instance, key wgtypes.Key) (Snapshot, bool, error) {
	name := m.Name(inst.ID)
	state, err := m.plane.EnsureLink(ctx, LinkSpec{Name: name, MTU: linkMTU(inst.MTU)})
	if err != nil {
		return Snapshot{}, false, m.note(err)
	}
	snap, err := m.plane.Snapshot(ctx, name)
	if err != nil {
		return Snapshot{}, false, m.note(err)
	}
	if !snap.Exists {
		return Snapshot{}, false, fmt.Errorf("%w: %s", ErrNoDevice, name)
	}
	// A link the panel has just made holds no key, no port and no addresses, so
	// every setting is written again rather than diffed against an empty device.
	if delta := diffDevice(snap.Device, key, inst.Port, inst.FWMark, state.Created); !delta.empty() {
		if err := m.configure(ctx, name, delta, nil); err != nil {
			return snap, state.Created, err
		}
	}
	return snap, state.Created, nil
}

// syncAddrs converges the device's own addresses and names the connected routes
// the kernel dropped with each address it removed.
func (m *Manager) syncAddrs(ctx context.Context, name string, current, desired []netip.Prefix) ([]netip.Prefix, []error) {
	add, del := diffAddrs(current, desired)
	var dropped []netip.Prefix
	var failures []error
	for _, p := range add {
		if err := m.plane.AddAddr(ctx, name, p); err != nil {
			failures = append(failures, fmt.Errorf("wireguard: add %s to %s: %w", p, name, err))
		}
	}
	for _, p := range del {
		if err := m.plane.DelAddr(ctx, name, p); err != nil {
			failures = append(failures, fmt.Errorf("wireguard: drop %s from %s: %w", p, name, err))
			continue
		}
		dropped = append(dropped, p.Masked())
	}
	return dropped, failures
}

// syncRoutes converges the peer routes. gone are the routes the address sync just
// took with it: they are off the device already, so deleting them answers ESRCH.
func (m *Manager) syncRoutes(ctx context.Context, name string, current, desired, addrs, gone []netip.Prefix) []error {
	add, del := diffRoutes(current, desired, addrs)
	var failures []error
	for _, p := range add {
		if err := m.plane.AddRoute(ctx, name, p); err != nil {
			failures = append(failures, fmt.Errorf("wireguard: route %s via %s: %w", p, name, err))
		}
	}
	for _, p := range del {
		if slices.Contains(gone, p) {
			continue
		}
		if err := m.plane.DelRoute(ctx, name, p); err != nil {
			failures = append(failures, fmt.Errorf("wireguard: unroute %s via %s: %w", p, name, err))
		}
	}
	return failures
}

// Reconcile drives the managed set toward desired, deleting the device of every
// inbound left out. retain spares one whose desired state could not be built.
func (m *Manager) Reconcile(ctx context.Context, desired []Instance, retain ...int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	want := make(map[int]struct{}, len(desired)+len(retain))
	for _, inst := range desired {
		want[inst.ID] = struct{}{}
	}
	// A device is deleted only on an answer, never on a failure to read one.
	for _, id := range retain {
		want[id] = struct{}{}
	}
	var failures []error
	unwanted := make(map[int]struct{}, len(m.devices))
	for id := range m.devices {
		if _, ok := want[id]; !ok {
			unwanted[id] = struct{}{}
		}
	}
	// m.devices is empty in every new process, so a device whose inbound left the
	// desired set while the panel was down is only findable on the host itself.
	names, err := m.plane.Links(ctx)
	if err != nil {
		failures = append(failures, m.note(err))
	}
	for _, name := range names {
		id, mine := m.owned(name)
		if !mine {
			continue
		}
		if _, keep := want[id]; !keep {
			unwanted[id] = struct{}{}
		}
	}
	for id := range unwanted {
		if err := m.removeLocked(ctx, id); err != nil {
			failures = append(failures, err)
		}
	}
	for _, inst := range desired {
		if err := m.ensureLocked(ctx, inst); err != nil {
			logger.Warningf("wireguard: reconcile failed for inbound %d: %v", inst.ID, err)
			failures = append(failures, fmt.Errorf("inbound %d: %w", inst.ID, err))
		}
	}
	return errors.Join(failures...)
}

// Remove deletes an inbound's device and forgets its counters. A device that is
// already gone is not an error: an inbound can be dropped twice.
func (m *Manager) Remove(ctx context.Context, id int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.removeLocked(ctx, id)
}

// removeLocked keeps the record when the delete fails, so the next reconcile
// tries again rather than leaving a device nothing is tracking.
func (m *Manager) removeLocked(ctx context.Context, id int) error {
	name := m.Name(id)
	rec, tracked := m.devices[id]
	// Deleting the link zeroes every peer counter, so this device's last interval
	// is banked first and handed out by the scrape that follows.
	if tracked {
		if snap, err := m.plane.Snapshot(ctx, name); err == nil && snap.Exists {
			m.drainLocked(rec, snap)
		}
	}
	if err := m.plane.DeleteLink(ctx, name); err != nil {
		return m.note(fmt.Errorf("wireguard: delete %s: %w", name, err))
	}
	// The record outlives its device until a scrape has taken what was banked;
	// dropping it here would throw that final interval away.
	if tracked && len(rec.pending) > 0 {
		return nil
	}
	delete(m.devices, id)
	logger.Infof("wireguard: removed device %s", name)
	return nil
}

// StopAll leaves every device up, unlike a core owning a sidecar. Bytes moved
// while the panel is down go unbilled; the next process baselines from the kernel.
func (m *Manager) StopAll(context.Context) error { return nil }

// AddPeer upserts one client's peer and writes nothing when the kernel already
// holds it, so a rename costs no handshake. It reads no other peer.
func (m *Manager) AddPeer(ctx context.Context, inst Instance, p Peer) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	name := m.Name(inst.ID)
	want, err := desiredPeer(p)
	if err != nil {
		return err
	}
	rec := m.record(inst.ID)
	// One peer per key, as desiredPeers enforces on the set: a second claimant
	// would take over the first one's counters and replace its allowedIPs.
	if other, taken := rec.byKey[want.PublicKey]; taken && other != p.Email {
		return fmt.Errorf("wireguard: clients %q and %q share one public key", other, p.Email)
	}
	snap, err := m.peerSnapshot(ctx, inst)
	if err != nil {
		return err
	}

	if inst.Tag != "" {
		rec.tag = inst.Tag
	}
	rec.track(p.Email, want.PublicKey)

	cfg, changed := upsertPeer(snap.Device.Peers, want)
	if !changed {
		return nil
	}
	if err := m.configure(ctx, name, deviceDelta{}, []wgtypes.PeerConfig{cfg}); err != nil {
		return err
	}
	addrs, err := parsePrefixes(inst.Address)
	if err != nil {
		return err
	}
	// Only this peer's routes are added; nothing is dropped, because the other
	// clients' prefixes are not in the instance a single-user add is handed.
	add, _ := diffRoutes(snap.Routes, desiredRoutes(addrs, []wgtypes.PeerConfig{cfg}), addrs)
	var failures []error
	for _, prefix := range add {
		if err := m.plane.AddRoute(ctx, name, prefix); err != nil {
			failures = append(failures, fmt.Errorf("wireguard: route %s via %s: %w", prefix, name, err))
		}
	}
	return errors.Join(failures...)
}

// RemovePeer deletes one client's peer. fallbackKey answers the email when the
// index is cold: the runtime removes a user without handing over the user set.
func (m *Manager) RemovePeer(ctx context.Context, inst Instance, email, fallbackKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	name := m.Name(inst.ID)
	key, ok := m.resolveLocked(inst, email, fallbackKey)
	if !ok {
		return nil
	}
	rec := m.record(inst.ID)
	snap, err := m.plane.Snapshot(ctx, name)
	if err != nil {
		return m.note(err)
	}
	if !snap.Exists {
		rec.forget(email, key)
		return nil
	}
	if inst.Tag != "" {
		rec.tag = inst.Tag
	}
	m.drainLocked(rec, snap)
	if err := m.configure(ctx, name, deviceDelta{}, []wgtypes.PeerConfig{{PublicKey: key, Remove: true}}); err != nil {
		return err
	}
	rec.forget(email, key)

	addrs, err := parsePrefixes(inst.Address)
	if err != nil {
		return err
	}
	// A caller that cannot name the device's addresses cannot tell a connected
	// route from a surplus one, and deleting one black-holes every client on it.
	if len(addrs) == 0 {
		return nil
	}
	remaining := make([]wgtypes.PeerConfig, 0, len(snap.Device.Peers))
	for _, peer := range snap.Device.Peers {
		if peer.PublicKey == key {
			continue
		}
		remaining = append(remaining, wgtypes.PeerConfig{PublicKey: peer.PublicKey, AllowedIPs: peer.AllowedIPs})
	}
	_, del := diffRoutes(snap.Routes, desiredRoutes(addrs, remaining), addrs)
	var failures []error
	for _, prefix := range del {
		if err := m.plane.DelRoute(ctx, name, prefix); err != nil {
			failures = append(failures, fmt.Errorf("wireguard: unroute %s via %s: %w", prefix, name, err))
		}
	}
	return errors.Join(failures...)
}

// resolveLocked maps a client email onto the public key its peer is keyed by: the
// live index first, then the instance, then the caller's fallback.
func (m *Manager) resolveLocked(inst Instance, email, fallbackKey string) (wgtypes.Key, bool) {
	if rec, ok := m.devices[inst.ID]; ok {
		if key, found := rec.byEmail[email]; found {
			return key, true
		}
	}
	for _, p := range inst.Peers {
		if p.Email != email {
			continue
		}
		if key, err := parseKey(p.PublicKey); err == nil && key != (wgtypes.Key{}) {
			return key, true
		}
	}
	key, err := parseKey(fallbackKey)
	if err != nil || key == (wgtypes.Key{}) {
		return wgtypes.Key{}, false
	}
	return key, true
}

// peerSnapshot reads the device a single-peer write targets. A device that is gone
// fails the edit: a rebuild from a single-user instance would serve nobody else.
func (m *Manager) peerSnapshot(ctx context.Context, inst Instance) (Snapshot, error) {
	name := m.Name(inst.ID)
	snap, err := m.plane.Snapshot(ctx, name)
	if err != nil {
		return Snapshot{}, m.note(err)
	}
	if !snap.Exists {
		return Snapshot{}, fmt.Errorf("%w: %s", ErrNoDevice, name)
	}
	return snap, nil
}

// CollectTraffic returns each client's delta since the previous scrape and who is
// online. Each device is read and billed as one step; see scrapeDevice.
func (m *Manager) CollectTraffic(ctx context.Context) ([]Traffic, []string) {
	m.scrapeMu.Lock()
	defer m.scrapeMu.Unlock()

	m.mu.Lock()
	ids := make([]int, 0, len(m.devices))
	for id := range m.devices {
		ids = append(ids, id)
	}
	m.mu.Unlock()

	var out []Traffic
	var online []string
	for _, id := range ids {
		billed, connected := m.scrapeDevice(ctx, id)
		out = append(out, billed...)
		online = append(online, connected...)
	}
	return out, online
}

// scrapeDevice reads and bills one device inside one lock. Split apart, a client
// removal between the two drops a baseline and re-bills the peer's whole lifetime.
func (m *Manager) scrapeDevice(ctx context.Context, id int) ([]Traffic, []string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rec, ok := m.devices[id]
	if !ok {
		return nil, nil
	}
	// Taken before the read can fail: a peer drained and then deleted with its
	// device would otherwise have its final reading dropped on the floor.
	billed := rec.takePending()
	snap, err := m.plane.Snapshot(ctx, m.Name(id))
	if err != nil || !snap.Exists {
		return flatten(billed), nil
	}
	var online []string
	for _, peer := range snap.Device.Peers {
		if email, known := rec.byKey[peer.PublicKey]; known && isOnline(peer.LastHandshakeTime) {
			online = append(online, email)
		}
	}
	readings, epoch := deviceReadings(snap, rec.generation)
	rec.attribute(rec.counter.Observe(epoch, readings), billed)
	return flatten(billed), online
}

func flatten(billed map[string]*Traffic) []Traffic {
	out := make([]Traffic, 0, len(billed))
	for _, acc := range billed {
		out = append(out, *acc)
	}
	return out
}

func isOnline(handshake time.Time) bool {
	return !handshake.IsZero() && time.Since(handshake) <= handshakeWindow
}
