package wireguard

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"

	wgutil "github.com/Arman2122/p-ui/internal/util/wireguard"
)

// Peer is one client as the kernel serves it: the public key it authenticates
// with and the tunnel addresses it is allowed to use.
type Peer struct {
	Email        string
	PublicKey    string
	PreSharedKey string
	AllowedIPs   []string
	KeepAlive    int
}

// Instance is the desired state of one kernel WireGuard inbound. Port is the
// inbound's own port; nothing reads a listen port out of the settings blob.
type Instance struct {
	ID         int
	Tag        string
	Port       int
	PrivateKey string
	Address    []string
	MTU        int
	FWMark     int
	Peers      []Peer
}

// interfacePrefix marks a device as this panel's. It is deliberately not "wg":
// an operator's own tunnel must never look like something the panel may delete.
const interfacePrefix = "pwg"

// InterfaceName is the kernel device an inbound is served by. It is derived from
// the id so two inbounds can never claim one device.
func InterfaceName(id int) string { return interfacePrefix + strconv.Itoa(id) }

// ownedID reads the inbound id back out of a device name. It round-trips through
// InterfaceName, so a near miss like pwg007 or pwg0 is somebody else's.
func ownedID(name string) (int, bool) {
	rest, ok := strings.CutPrefix(name, interfacePrefix)
	if !ok {
		return 0, false
	}
	id, err := strconv.Atoi(rest)
	if err != nil || id <= 0 || InterfaceName(id) != name {
		return 0, false
	}
	return id, true
}

// defaultMTU is what the wireguard module gives a new link: 1500 less its own
// 32-byte header, 8 for UDP and 40 for the outer IPv6 header.
const defaultMTU = 1420

// linkMTU resolves an absent MTU into the kernel's own default rather than
// leaving whatever the device holds, so clearing the field is an edit that lands.
func linkMTU(mtu int) int {
	if mtu <= 0 {
		return defaultMTU
	}
	return mtu
}

// Interface names the device this instance owns.
func (inst Instance) Interface() string { return InterfaceName(inst.ID) }

// deviceSettings is the device half of a wgkernel inbound's settings JSON. The
// clients array is deliberately absent: peers come from the contract's user set.
type deviceSettings struct {
	SecretKey string   `json:"secretKey"`
	Address   []string `json:"address"`
	MTU       int      `json:"mtu"`
	FWMark    int      `json:"fwmark"`
}

// ApplySettings fills the device half of an Instance from the inbound's stored
// settings, leaving Peers untouched.
func (inst *Instance) ApplySettings(settings string) error {
	if settings == "" {
		return nil
	}
	var parsed deviceSettings
	if err := json.Unmarshal([]byte(settings), &parsed); err != nil {
		return err
	}
	inst.PrivateKey = strings.TrimSpace(parsed.SecretKey)
	inst.Address = parsed.Address
	inst.MTU = parsed.MTU
	inst.FWMark = parsed.FWMark
	return nil
}

// PeerKeyFromSettings finds one client's public key in an inbound's stored
// settings. Lookup only: a peer set read from this blob resurrects dead clients.
func PeerKeyFromSettings(settings, email string) string {
	if settings == "" || email == "" {
		return ""
	}
	var parsed struct {
		Clients []struct {
			Email     string `json:"email"`
			PublicKey string `json:"publicKey"`
		} `json:"clients"`
	}
	if err := json.Unmarshal([]byte(settings), &parsed); err != nil {
		return ""
	}
	for _, c := range parsed.Clients {
		if c.Email == email {
			return strings.TrimSpace(c.PublicKey)
		}
	}
	return ""
}

// DeviceFingerprint moves whenever something outside the peer list changes. It
// answers whether an edit can be hot-applied and is never used to skip a read.
func (inst Instance) DeviceFingerprint() string {
	parts := []string{
		strconv.Itoa(inst.Port),
		inst.PrivateKey,
		strconv.Itoa(inst.MTU),
		strconv.Itoa(inst.FWMark),
		strings.Join(slices.Sorted(slices.Values(inst.Address)), ","),
	}
	return strings.Join(parts, "|")
}

// PeersFingerprint identifies the served peer set regardless of order, so a
// reordered clients array in the stored settings does not read as a change.
func (inst Instance) PeersFingerprint() string {
	rows := make([]string, 0, len(inst.Peers))
	for _, p := range inst.Peers {
		ips := slices.Sorted(slices.Values(p.AllowedIPs))
		rows = append(rows, fmt.Sprintf("%s;psk=%s;ips=%s;ka=%d", p.PublicKey, p.PreSharedKey, strings.Join(ips, ","), p.KeepAlive))
	}
	slices.Sort(rows)
	return strings.Join(rows, "|")
}

// parseKey decodes a WireGuard key. An empty key yields the zero value, which
// clears the setting rather than leaving whatever the device happened to hold.
func parseKey(s string) (wgtypes.Key, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return wgtypes.Key{}, nil
	}
	// Through the panel's own decoder so base64 and hex keys are accepted here
	// exactly as they are everywhere else.
	encoded, err := wgutil.KeyToHex(s)
	if err != nil {
		return wgtypes.Key{}, err
	}
	raw, err := hex.DecodeString(encoded)
	if err != nil {
		return wgtypes.Key{}, err
	}
	return wgtypes.NewKey(raw)
}

// parseAllowedIPs turns the panel's canonical allowedIPs into kernel prefixes. A
// bare address becomes its host prefix, and every prefix is masked as wg stores it.
func parseAllowedIPs(values []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		p, err := netip.ParsePrefix(v)
		if err != nil {
			addr, addrErr := netip.ParseAddr(v)
			if addrErr != nil {
				return nil, fmt.Errorf("wireguard: invalid allowedIPs entry %q", v)
			}
			p = netip.PrefixFrom(addr, addr.BitLen())
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

// parsePrefixes reads the device's own tunnel addresses. Unlike allowedIPs these
// keep their host part: 10.0.0.1/24 is an address, 10.0.0.0/24 is a route.
func parsePrefixes(values []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		p, err := netip.ParsePrefix(v)
		if err != nil {
			addr, addrErr := netip.ParseAddr(v)
			if addrErr != nil {
				return nil, fmt.Errorf("wireguard: invalid device address %q", v)
			}
			p = netip.PrefixFrom(addr, addr.BitLen())
		}
		out = append(out, p)
	}
	return out, nil
}

// desiredPeer builds the kernel configuration for one client. Every optional
// field is written rather than omitted, so an out-of-band change is overwritten.
func desiredPeer(p Peer) (wgtypes.PeerConfig, error) {
	key, err := parseKey(p.PublicKey)
	if err != nil {
		return wgtypes.PeerConfig{}, fmt.Errorf("wireguard: client %q has an unusable public key: %w", p.Email, err)
	}
	if key == (wgtypes.Key{}) {
		return wgtypes.PeerConfig{}, fmt.Errorf("wireguard: client %q has no public key", p.Email)
	}
	psk, err := parseKey(p.PreSharedKey)
	if err != nil {
		return wgtypes.PeerConfig{}, fmt.Errorf("wireguard: client %q has an unusable pre-shared key: %w", p.Email, err)
	}
	allowed, err := parseAllowedIPs(p.AllowedIPs)
	if err != nil {
		return wgtypes.PeerConfig{}, err
	}
	keepalive := time.Duration(p.KeepAlive) * time.Second
	cfg := wgtypes.PeerConfig{
		PublicKey:                   key,
		PresharedKey:                &psk,
		PersistentKeepaliveInterval: &keepalive,
		ReplaceAllowedIPs:           true,
		AllowedIPs:                  make([]net.IPNet, 0, len(allowed)),
	}
	for _, prefix := range allowed {
		cfg.AllowedIPs = append(cfg.AllowedIPs, toIPNet(prefix))
	}
	return cfg, nil
}

// desiredPeers converts the served set, returning the peers that parse alongside
// an error naming the rest. One bad client must not freeze the whole inbound.
func desiredPeers(peers []Peer) ([]wgtypes.PeerConfig, error) {
	out := make([]wgtypes.PeerConfig, 0, len(peers))
	seen := make(map[wgtypes.Key]string, len(peers))
	claimed := make(map[string]string, len(peers))
	var rejected []error
	for _, p := range peers {
		cfg, err := desiredPeer(p)
		if err != nil {
			rejected = append(rejected, err)
			continue
		}
		// The kernel holds one peer per key, so the later claimant is dropped
		// rather than handed the first one's counters and allowedIPs.
		if other, dup := seen[cfg.PublicKey]; dup {
			rejected = append(rejected, fmt.Errorf("wireguard: clients %q and %q share one public key", other, p.Email))
			continue
		}
		if prefix, other := claimedAllowedIP(claimed, cfg.AllowedIPs); other != "" {
			rejected = append(rejected, fmt.Errorf("wireguard: clients %q and %q share allowed-IP %s", other, p.Email, prefix))
			continue
		}
		seen[cfg.PublicKey] = p.Email
		for _, n := range cfg.AllowedIPs {
			claimed[n.String()] = p.Email
		}
		out = append(out, cfg)
	}
	return out, errors.Join(rejected...)
}

// claimedAllowedIP names the client already allowed one of these prefixes. The
// kernel MOVES a shared prefix to the later peer, so pushing both never converges.
func claimedAllowedIP(claimed map[string]string, allowed []net.IPNet) (string, string) {
	for _, n := range allowed {
		if other, taken := claimed[n.String()]; taken {
			return n.String(), other
		}
	}
	return "", ""
}

// peerEqual reports whether the kernel already holds exactly this peer. Endpoint
// and handshake state are the peer's own and are never part of desired state.
func peerEqual(cur wgtypes.Peer, want wgtypes.PeerConfig) bool {
	if want.PresharedKey == nil || want.PersistentKeepaliveInterval == nil {
		return false
	}
	if cur.PresharedKey != *want.PresharedKey || cur.PersistentKeepaliveInterval != *want.PersistentKeepaliveInterval {
		return false
	}
	if len(cur.AllowedIPs) != len(want.AllowedIPs) {
		return false
	}
	have := make([]string, 0, len(cur.AllowedIPs))
	for _, n := range cur.AllowedIPs {
		have = append(have, n.String())
	}
	wanted := make([]string, 0, len(want.AllowedIPs))
	for _, n := range want.AllowedIPs {
		wanted = append(wanted, n.String())
	}
	slices.Sort(have)
	slices.Sort(wanted)
	return slices.Equal(have, wanted)
}

// diffPeers reports the peer writes that converge the device: an upsert for each
// peer that differs and a removal for each one no longer served.
func diffPeers(current []wgtypes.Peer, desired []wgtypes.PeerConfig, full bool) []wgtypes.PeerConfig {
	have := make(map[wgtypes.Key]wgtypes.Peer, len(current))
	for _, p := range current {
		have[p.PublicKey] = p
	}
	want := make(map[wgtypes.Key]struct{}, len(desired))
	var out []wgtypes.PeerConfig
	for _, cfg := range desired {
		want[cfg.PublicKey] = struct{}{}
		cur, present := have[cfg.PublicKey]
		if !full && present && peerEqual(cur, cfg) {
			continue
		}
		out = append(out, cfg)
	}
	for _, p := range current {
		if _, served := want[p.PublicKey]; !served {
			out = append(out, wgtypes.PeerConfig{PublicKey: p.PublicKey, Remove: true})
		}
	}
	return out
}

// upsertPeer returns the write that makes the kernel hold want, and false when it
// already does. Used where only one client changed, so no other peer is read.
func upsertPeer(current []wgtypes.Peer, want wgtypes.PeerConfig) (wgtypes.PeerConfig, bool) {
	for _, p := range current {
		if p.PublicKey == want.PublicKey {
			return want, !peerEqual(p, want)
		}
	}
	return want, true
}

// deviceDelta is the scalar half of a configuration push. A nil field is a
// setting the device already holds.
type deviceDelta struct {
	PrivateKey   *wgtypes.Key
	ListenPort   *int
	FirewallMark *int
}

func (d deviceDelta) empty() bool {
	return d.PrivateKey == nil && d.ListenPort == nil && d.FirewallMark == nil
}

// diffDevice reports the device scalars that must be written. full re-pushes all
// of them, because a link the panel has just created holds none.
func diffDevice(cur wgtypes.Device, key wgtypes.Key, port, fwmark int, full bool) deviceDelta {
	var d deviceDelta
	if full || cur.PrivateKey != key {
		d.PrivateKey = &key
	}
	if full || cur.ListenPort != port {
		d.ListenPort = &port
	}
	if full || cur.FirewallMark != fwmark {
		d.FirewallMark = &fwmark
	}
	return d
}

// diffAddrs reports the device addresses to add and to drop. IPv6 link-local
// addresses are the kernel's own doing and are never touched.
func diffAddrs(current, desired []netip.Prefix) (add, del []netip.Prefix) {
	have := make(map[string]struct{}, len(current))
	for _, p := range current {
		if p.Addr().IsLinkLocalUnicast() {
			continue
		}
		have[p.String()] = struct{}{}
	}
	want := make(map[string]struct{}, len(desired))
	for _, p := range desired {
		want[p.String()] = struct{}{}
		if _, ok := have[p.String()]; !ok {
			add = append(add, p)
		}
	}
	for _, p := range current {
		if p.Addr().IsLinkLocalUnicast() {
			continue
		}
		if _, ok := want[p.String()]; !ok {
			del = append(del, p)
		}
	}
	return add, del
}

// coveredByAddrs reports whether one of the device's own addresses already makes
// the kernel route this prefix over the device.
func coveredByAddrs(p netip.Prefix, addrs []netip.Prefix) bool {
	for _, a := range addrs {
		network := a.Masked()
		if network.Bits() <= p.Bits() && network.Contains(p.Addr()) {
			return true
		}
	}
	return false
}

// kernelOwned reports a route the panel must never delete: a connected route one
// of the device's addresses installed, or the kernel's own link-local.
func kernelOwned(p netip.Prefix, addrs []netip.Prefix) bool {
	return p.Addr().IsLinkLocalUnicast() || coveredByAddrs(p, addrs)
}

// desiredRoutes names the peer prefixes no device address already routes; wgctrl
// installs none, so those peers black-hole. A default prefix is never routed.
func desiredRoutes(addrs []netip.Prefix, peers []wgtypes.PeerConfig) []netip.Prefix {
	var out []netip.Prefix
	seen := make(map[string]struct{})
	for _, peer := range peers {
		if peer.Remove {
			continue
		}
		for _, n := range peer.AllowedIPs {
			prefix, ok := toPrefix(&n)
			if !ok || prefix.Bits() == 0 || coveredByAddrs(prefix, addrs) {
				continue
			}
			if _, dup := seen[prefix.String()]; dup {
				continue
			}
			seen[prefix.String()] = struct{}{}
			out = append(out, prefix)
		}
	}
	return out
}

// diffRoutes reports the peer routes to add and to drop. A prefix an address
// covers is the kernel's connected route and fe80::/64 is its own; both stay.
func diffRoutes(current, desired, addrs []netip.Prefix) (add, del []netip.Prefix) {
	have := make(map[string]struct{}, len(current))
	for _, p := range current {
		if kernelOwned(p, addrs) {
			continue
		}
		have[p.String()] = struct{}{}
	}
	want := make(map[string]struct{}, len(desired))
	for _, p := range desired {
		want[p.String()] = struct{}{}
		if _, ok := have[p.String()]; !ok {
			add = append(add, p)
		}
	}
	for _, p := range current {
		if kernelOwned(p, addrs) {
			continue
		}
		if _, ok := want[p.String()]; !ok {
			del = append(del, p)
		}
	}
	return add, del
}

// toIPNet converts a prefix into the shape wgctrl and netlink take.
func toIPNet(p netip.Prefix) net.IPNet {
	return net.IPNet{
		IP:   net.IP(p.Addr().AsSlice()),
		Mask: net.CIDRMask(p.Bits(), p.Addr().BitLen()),
	}
}

// toPrefix converts back, unmapping a 4-in-6 address so the two forms of one
// IPv4 prefix compare equal.
func toPrefix(n *net.IPNet) (netip.Prefix, bool) {
	if n == nil || n.IP == nil {
		return netip.Prefix{}, false
	}
	addr, ok := netip.AddrFromSlice(n.IP)
	if !ok {
		return netip.Prefix{}, false
	}
	addr = addr.Unmap()
	ones, _ := n.Mask.Size()
	if ones > addr.BitLen() {
		return netip.Prefix{}, false
	}
	return netip.PrefixFrom(addr, ones), true
}
