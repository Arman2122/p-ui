// Package wgtest is an in-memory stand-in for the host network stack, honest
// about what the kernel really does to peers, routes and byte counters.
package wgtest

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	"github.com/Arman2122/p-ui/internal/wireguard"
)

var _ wireguard.Plane = (*Kernel)(nil)

// counter is one peer's cumulative byte total plus the handshake that makes its
// client count as online. It is keyed by public key, exactly as the kernel is.
type counter struct {
	up        int64
	down      int64
	handshake time.Time
}

type link struct {
	index      int
	mtu        int
	up         bool
	privateKey wgtypes.Key
	listenPort int
	fwmark     int
	peers      map[wgtypes.Key]*wgtypes.Peer
	counters   map[wgtypes.Key]*counter
	addrs      []netip.Prefix
	routes     []netip.Prefix
}

// Kernel is the fake network stack. Every counter it exposes is there so a test
// can assert what did NOT happen — that the link never went down, above all.
type Kernel struct {
	mu        sync.Mutex
	links     map[string]*link
	nextIndex int

	LinkCreates int
	LinkDeletes int
	Configures  int
	// Configs records every push in order, so a test can prove ReplacePeers is
	// never set: it would wipe every peer the diff did not mention.
	Configs []wgtypes.Config

	// ProbeErr is what Probe answers, so a preflight failure can be driven.
	ProbeErr error
}

// New returns an empty stand-in with no devices on it.
func New() *Kernel {
	return &Kernel{links: map[string]*link{}, nextIndex: 10}
}

func (k *Kernel) Probe(context.Context) error { return k.ProbeErr }

func (k *Kernel) Snapshot(_ context.Context, name string) (wireguard.Snapshot, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	l, ok := k.links[name]
	if !ok {
		return wireguard.Snapshot{}, nil
	}
	return wireguard.Snapshot{
		Exists: true,
		Link:   wireguard.LinkState{Index: l.index, MTU: l.mtu, Up: l.up},
		Device: l.device(name),
		Addrs:  slices.Clone(l.addrs),
		Routes: slices.Clone(l.routes),
	}, nil
}

// device renders what wgctrl would report. A key with counters but no peer entry
// becomes a zero-config peer: coretest feeds traffic with no reconcile after it.
func (l *link) device(name string) wgtypes.Device {
	dev := wgtypes.Device{
		Name:         name,
		Type:         wgtypes.LinuxKernel,
		PrivateKey:   l.privateKey,
		ListenPort:   l.listenPort,
		FirewallMark: l.fwmark,
	}
	for key, p := range l.peers {
		peer := *p
		peer.AllowedIPs = slices.Clone(p.AllowedIPs)
		if c := l.counters[key]; c != nil {
			peer.ReceiveBytes, peer.TransmitBytes, peer.LastHandshakeTime = c.up, c.down, c.handshake
		}
		dev.Peers = append(dev.Peers, peer)
	}
	for key, c := range l.counters {
		if _, configured := l.peers[key]; configured {
			continue
		}
		dev.Peers = append(dev.Peers, wgtypes.Peer{
			PublicKey:         key,
			ReceiveBytes:      c.up,
			TransmitBytes:     c.down,
			LastHandshakeTime: c.handshake,
		})
	}
	slices.SortFunc(dev.Peers, func(a, b wgtypes.Peer) int {
		return strings.Compare(a.PublicKey.String(), b.PublicKey.String())
	})
	return dev
}

// Links names every interface on the stand-in. All of them are WireGuard links,
// so the type filter the real one applies has nothing to exclude here.
func (k *Kernel) Links(context.Context) ([]string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	out := make([]string, 0, len(k.links))
	for name := range k.links {
		out = append(out, name)
	}
	slices.Sort(out)
	return out, nil
}

func (k *Kernel) EnsureLink(_ context.Context, spec wireguard.LinkSpec) (wireguard.LinkState, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	l, ok := k.links[spec.Name]
	if !ok {
		k.nextIndex++
		k.LinkCreates++
		l = &link{
			index:    k.nextIndex,
			mtu:      spec.MTU,
			up:       true,
			peers:    map[wgtypes.Key]*wgtypes.Peer{},
			counters: map[wgtypes.Key]*counter{},
		}
		k.links[spec.Name] = l
		return wireguard.LinkState{Index: l.index, MTU: l.mtu, Up: true, Created: true}, nil
	}
	if spec.MTU > 0 {
		l.mtu = spec.MTU
	}
	l.up = true
	return wireguard.LinkState{Index: l.index, MTU: l.mtu, Up: true}, nil
}

func (k *Kernel) DeleteLink(_ context.Context, name string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if _, ok := k.links[name]; !ok {
		return nil
	}
	delete(k.links, name)
	k.LinkDeletes++
	return nil
}

// Configure applies one push with the kernel's semantics: ReplacePeers wipes the
// set, Remove takes the counters, UpdateOnly skips an absent peer, a re-add zeroes.
func (k *Kernel) Configure(_ context.Context, name string, cfg wgtypes.Config) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	l, ok := k.links[name]
	if !ok {
		return fmt.Errorf("%w: %s", wireguard.ErrNoDevice, name)
	}
	k.Configures++
	k.Configs = append(k.Configs, cfg)

	if cfg.PrivateKey != nil {
		l.privateKey = *cfg.PrivateKey
	}
	if cfg.ListenPort != nil {
		l.listenPort = *cfg.ListenPort
	}
	if cfg.FirewallMark != nil {
		l.fwmark = *cfg.FirewallMark
	}
	if cfg.ReplacePeers {
		l.peers = map[wgtypes.Key]*wgtypes.Peer{}
		l.counters = map[wgtypes.Key]*counter{}
	}
	for _, pc := range cfg.Peers {
		cur, present := l.peers[pc.PublicKey]
		switch {
		case pc.Remove:
			delete(l.peers, pc.PublicKey)
			delete(l.counters, pc.PublicKey)
			continue
		case !present && pc.UpdateOnly:
			continue
		case !present:
			cur = &wgtypes.Peer{PublicKey: pc.PublicKey}
			l.peers[pc.PublicKey] = cur
			delete(l.counters, pc.PublicKey)
		}
		if pc.PresharedKey != nil {
			cur.PresharedKey = *pc.PresharedKey
		}
		if pc.PersistentKeepaliveInterval != nil {
			cur.PersistentKeepaliveInterval = *pc.PersistentKeepaliveInterval
		}
		if pc.Endpoint != nil {
			cur.Endpoint = pc.Endpoint
		}
		if pc.ReplaceAllowedIPs {
			cur.AllowedIPs = nil
		}
		cur.AllowedIPs = append(cur.AllowedIPs, pc.AllowedIPs...)
	}
	return nil
}

// AddAddr also installs the connected route the kernel derives from an address,
// so the route diff faces the same set a real device would show it.
func (k *Kernel) AddAddr(_ context.Context, name string, addr netip.Prefix) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	l, ok := k.links[name]
	if !ok {
		return fmt.Errorf("%w: %s", wireguard.ErrNoDevice, name)
	}
	if slices.Contains(l.addrs, addr) {
		return fmt.Errorf("wgtest: %s already carries %s", name, addr)
	}
	l.addrs = append(l.addrs, addr)
	if connected := addr.Masked(); !slices.Contains(l.routes, connected) {
		l.routes = append(l.routes, connected)
	}
	return nil
}

func (k *Kernel) DelAddr(_ context.Context, name string, addr netip.Prefix) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	l, ok := k.links[name]
	if !ok {
		return nil
	}
	l.addrs = slices.DeleteFunc(l.addrs, func(p netip.Prefix) bool { return p == addr })
	connected := addr.Masked()
	l.routes = slices.DeleteFunc(l.routes, func(p netip.Prefix) bool { return p == connected })
	return nil
}

func (k *Kernel) AddRoute(_ context.Context, name string, dst netip.Prefix) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	l, ok := k.links[name]
	if !ok {
		return fmt.Errorf("%w: %s", wireguard.ErrNoDevice, name)
	}
	// The kernel answers EEXIST rather than quietly accepting the same route
	// twice, so a diff that re-adds every reconcile fails loudly here.
	if slices.Contains(l.routes, dst) {
		return fmt.Errorf("wgtest: %s already routes %s", name, dst)
	}
	l.routes = append(l.routes, dst)
	return nil
}

func (k *Kernel) DelRoute(_ context.Context, name string, dst netip.Prefix) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	l, ok := k.links[name]
	if !ok {
		return nil
	}
	// The kernel answers ESRCH for a route that is not there, so a diff computed
	// against a stale snapshot fails loudly here instead of passing quietly.
	if !slices.Contains(l.routes, dst) {
		return fmt.Errorf("wgtest: %s does not route %s", name, dst)
	}
	l.routes = slices.DeleteFunc(l.routes, func(p netip.Prefix) bool { return p == dst })
	return nil
}

// FeedTraffic sets a peer's cumulative counters and stamps a fresh handshake. It
// writes whether or not the peer is configured, so a post-recreate reading lands.
func (k *Kernel) FeedTraffic(name string, key wgtypes.Key, up, down int64) {
	k.mu.Lock()
	defer k.mu.Unlock()
	l, ok := k.links[name]
	if !ok {
		return
	}
	l.counters[key] = &counter{up: up, down: down, handshake: time.Now()}
}

// RecreateLink models `ip link del` followed by `ip link add`: a new ifindex and
// a device with no key, no port, no addresses, no routes, no peers, no counters.
func (k *Kernel) RecreateLink(name string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if _, ok := k.links[name]; !ok {
		return
	}
	k.LinkDeletes++
	k.LinkCreates++
	k.nextIndex++
	k.links[name] = &link{
		index:    k.nextIndex,
		up:       true,
		peers:    map[wgtypes.Key]*wgtypes.Peer{},
		counters: map[wgtypes.Key]*counter{},
	}
}

// FlushPeers models an operator running `wg set <dev> peer ... remove` outside
// the panel: the peers and their counters go, the device itself stays up.
func (k *Kernel) FlushPeers(name string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	l, ok := k.links[name]
	if !ok {
		return
	}
	l.peers = map[wgtypes.Key]*wgtypes.Peer{}
	l.counters = map[wgtypes.Key]*counter{}
}

// Exists reports whether the device is there.
func (k *Kernel) Exists(name string) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	_, ok := k.links[name]
	return ok
}

// Index is the device's ifindex, which changes across a recreate.
func (k *Kernel) Index(name string) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	if l, ok := k.links[name]; ok {
		return l.index
	}
	return 0
}

// Device is what wgctrl would report for the named interface.
func (k *Kernel) Device(name string) wgtypes.Device {
	k.mu.Lock()
	defer k.mu.Unlock()
	if l, ok := k.links[name]; ok {
		return l.device(name)
	}
	return wgtypes.Device{}
}

// PeerKeys names the peers the device serves, sorted, without the synthesised
// counter-only entries Device reports.
func (k *Kernel) PeerKeys(name string) []wgtypes.Key {
	k.mu.Lock()
	defer k.mu.Unlock()
	l, ok := k.links[name]
	if !ok {
		return nil
	}
	out := make([]wgtypes.Key, 0, len(l.peers))
	for key := range l.peers {
		out = append(out, key)
	}
	slices.SortFunc(out, func(a, b wgtypes.Key) int { return strings.Compare(a.String(), b.String()) })
	return out
}

// AllowedIPs are the prefixes one peer is configured with.
func (k *Kernel) AllowedIPs(name string, key wgtypes.Key) []net.IPNet {
	k.mu.Lock()
	defer k.mu.Unlock()
	l, ok := k.links[name]
	if !ok {
		return nil
	}
	if p, present := l.peers[key]; present {
		return slices.Clone(p.AllowedIPs)
	}
	return nil
}

// Addrs are the device's own tunnel addresses.
func (k *Kernel) Addrs(name string) []netip.Prefix {
	k.mu.Lock()
	defer k.mu.Unlock()
	if l, ok := k.links[name]; ok {
		return slices.Clone(l.addrs)
	}
	return nil
}

// Routes are every prefix routed over the device, connected routes included.
func (k *Kernel) Routes(name string) []netip.Prefix {
	k.mu.Lock()
	defer k.mu.Unlock()
	if l, ok := k.links[name]; ok {
		return slices.Clone(l.routes)
	}
	return nil
}

// PrivateKey is the device key, zero on a device that has never been configured.
func (k *Kernel) PrivateKey(name string) wgtypes.Key {
	k.mu.Lock()
	defer k.mu.Unlock()
	if l, ok := k.links[name]; ok {
		return l.privateKey
	}
	return wgtypes.Key{}
}

// ListenPort is the UDP port the device is bound to.
func (k *Kernel) ListenPort(name string) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	if l, ok := k.links[name]; ok {
		return l.listenPort
	}
	return 0
}

// MTU is the device's link MTU.
func (k *Kernel) MTU(name string) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	if l, ok := k.links[name]; ok {
		return l.mtu
	}
	return 0
}
