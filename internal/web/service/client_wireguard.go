package service

import (
	"encoding/json"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/util/common"
	wgutil "github.com/Arman2122/p-ui/internal/util/wireguard"
)

const defaultWireguardBase = "10.0.0.0/24"

// clientCarriesWireguardKeys asks the kind's core whether its clients hold
// WireGuard key material, so a second core answering yes needs no edit here.
func clientCarriesWireguardKeys(protocol model.Protocol) bool {
	return slices.Contains(cores.ClientCredentials(core.Kind(protocol)), core.CredAllowedIPs)
}

// ownsHostWireguardDevice reports a kind that puts a real WireGuard interface on
// this host: WireGuard clients, and no Xray config the panel converges instead.
func ownsHostWireguardDevice(protocol model.Protocol) bool {
	return clientCarriesWireguardKeys(protocol) && !cores.ServedByXray(core.Kind(protocol))
}

// hostWireguardProtocols are the kinds whose devices share the one host routing
// table, and so are the only ones a tunnel address can collide with.
func hostWireguardProtocols() []string {
	out := make([]string, 0, 1)
	for _, kind := range cores.Kinds() {
		if ownsHostWireguardDevice(model.Protocol(kind)) {
			out = append(out, string(kind))
		}
	}
	return out
}

// wireguardDeviceAddresses reads the tunnel addresses an inbound puts on its own
// device. An entry that will not parse is skipped; the engine rejects it loudly.
func wireguardDeviceAddresses(settings string) []netip.Prefix {
	var parsed struct {
		Address []string `json:"address"`
	}
	if settings == "" || json.Unmarshal([]byte(settings), &parsed) != nil {
		return nil
	}
	out := make([]netip.Prefix, 0, len(parsed.Address))
	for _, v := range parsed.Address {
		if p, err := netip.ParsePrefix(strings.TrimSpace(v)); err == nil {
			out = append(out, p)
		}
	}
	return out
}

// checkWireguardAddressConflict rejects two host devices sharing a tunnel subnet.
// Both install the same connected route into one routing table, so one inbound's
// client is answered over the other's device and encrypted to the wrong peer.
func (s *InboundService) checkWireguardAddressConflict(inbound *model.Inbound, ignoreId int) error {
	if !ownsHostWireguardDevice(inbound.Protocol) {
		return nil
	}
	want := wireguardDeviceAddresses(inbound.Settings)
	if len(want) == 0 {
		return nil
	}
	q := database.GetDB().Model(model.Inbound{}).Where("protocol IN ?", hostWireguardProtocols())
	if ignoreId > 0 {
		q = q.Where("id != ?", ignoreId)
	}
	var candidates []*model.Inbound
	if err := q.Find(&candidates).Error; err != nil {
		return err
	}
	for _, c := range candidates {
		if !sameNode(c.NodeID, inbound.NodeID) {
			continue
		}
		for _, taken := range wireguardDeviceAddresses(c.Settings) {
			for _, p := range want {
				if taken.Overlaps(p) {
					return common.NewError("wireguard: address", p.String(), "overlaps", taken.String(), "already served by inbound", inboundName(c))
				}
			}
		}
	}
	return nil
}

/*
checkWireguardAddressCoverage refuses an interface-address edit that would strand
the clients it already allocated.

Rebasing 10.90.0.1/24 to 10.91.0.1/24, or narrowing a /16 to a /24, fails
INVISIBLY today: desiredRoutes installs a per-peer route for anything the device
prefix no longer covers, so every client keeps working while the addressing plan
stops meaning anything and the .conf files already on their devices no longer
relate to the interface. Refused rather than migrated, because a server cannot
reach into a config a user is already holding: renumbering silently would break
exactly the people it was trying to keep working. Widening always passes.

Only a client the OLD prefixes covered counts. An inbound whose pool widened past
its own /24 has clients outside it already, and holding an edit hostage to those
would refuse edits that fix nothing.
*/
func checkWireguardAddressCoverage(oldInbound, inbound *model.Inbound) error {
	if !ownsHostWireguardDevice(inbound.Protocol) {
		return nil
	}
	before := wireguardDeviceAddresses(oldInbound.Settings)
	after := wireguardDeviceAddresses(inbound.Settings)
	if len(before) == 0 || len(after) == 0 {
		return nil
	}
	for _, raw := range wireguardClientAddresses(inbound.Settings) {
		held, err := netip.ParsePrefix(strings.TrimSpace(raw))
		if err != nil || !wireguardCovers(before, held) || wireguardCovers(after, held) {
			continue
		}
		return common.NewError("wireguard: client address", held.String(),
			"is inside the interface address it was allocated from and outside every address this edit leaves;",
			"widen the interface address instead, or remove the clients first — their .conf files carry the old one")
	}
	return nil
}

// wireguardCovers mirrors the engine's coveredByAddrs: a prefix is covered when
// a device address is no narrower and contains it.
func wireguardCovers(addrs []netip.Prefix, p netip.Prefix) bool {
	for _, a := range addrs {
		network := a.Masked()
		if network.Bits() <= p.Bits() && network.Contains(p.Addr()) {
			return true
		}
	}
	return false
}

// wireguardClientAddresses reads the tunnel addresses an inbound's clients hold.
// On a kernel device each one is a real route in the ONE host routing table.
func wireguardClientAddresses(settings string) []string {
	var parsed struct {
		Clients []struct {
			AllowedIPs []string `json:"allowedIPs"`
		} `json:"clients"`
	}
	if settings == "" || json.Unmarshal([]byte(settings), &parsed) != nil {
		return nil
	}
	var out []string
	for _, c := range parsed.Clients {
		out = append(out, c.AllowedIPs...)
	}
	return out
}

/*
wireguardPool is where one inbound may allocate its clients' tunnel addresses.

A kernel WireGuard inbound's client addresses are host-global: every one is a
route in the same table, so an address two inbounds both hand out sends the
second customer's return traffic into the first customer's tunnel. The pool is
therefore this inbound's own subnet minus everything another host device owns,
never a package-wide default two inbounds would allocate from independently.
*/
type wireguardPool struct {
	base string
	// taken are the addresses other host inbounds' clients already hold.
	taken []string
	// blocked are the subnets other host devices already answer for.
	blocked []netip.Prefix
}

func (s *InboundService) wireguardPoolFor(inbound *model.Inbound) (wireguardPool, error) {
	pool := wireguardPool{base: defaultWireguardBase}
	if own := wireguardDeviceAddresses(inbound.Settings); len(own) > 0 {
		pool.base = own[0].Masked().String()
	}
	if !ownsHostWireguardDevice(inbound.Protocol) {
		return pool, nil
	}
	q := database.GetDB().Model(model.Inbound{}).Where("protocol IN ?", hostWireguardProtocols())
	if inbound.Id > 0 {
		q = q.Where("id != ?", inbound.Id)
	}
	var candidates []*model.Inbound
	if err := q.Find(&candidates).Error; err != nil {
		return pool, err
	}
	for _, c := range candidates {
		if !sameNode(c.NodeID, inbound.NodeID) {
			continue
		}
		for _, device := range wireguardDeviceAddresses(c.Settings) {
			pool.blocked = append(pool.blocked, device.Masked())
		}
		pool.taken = append(pool.taken, wireguardClientAddresses(c.Settings)...)
	}
	return pool, nil
}

// inboundName is how an inbound is named back to the operator in a conflict.
func inboundName(in *model.Inbound) string {
	if in.Remark != "" {
		return in.Remark
	}
	if in.Tag != "" {
		return in.Tag
	}
	return "#" + strconv.Itoa(in.Id)
}

// wireguardKeyCollision names the other client already authorised by this public
// key. The kernel serves one peer per key, so a second claimant has no peer of
// its own and is billed the first one's traffic.
func wireguardKeyCollision(publicKey string, others []model.Client, skip int) string {
	publicKey = strings.TrimSpace(publicKey)
	if publicKey == "" {
		return ""
	}
	for i := range others {
		if i == skip || strings.TrimSpace(others[i].PublicKey) != publicKey {
			continue
		}
		return others[i].Email
	}
	return ""
}

func wireguardHostAddr(s string) netip.Addr {
	s = strings.TrimSpace(s)
	if s == "" {
		return netip.Addr{}
	}
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Addr()
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return a
	}
	return netip.Addr{}
}

/*
wireguardAllocationBase picks the prefix new clients are allocated from.

The inbound's OWN device prefix wins whenever it already covers this inbound's
clients: it is the size the operator asked for, and deriving a /24 from a client
instead strands everything past the 255th outside a prefix that still has room —
a /22 spilled to the surrounding /16 with 768 of its own addresses free, and each
of those clients then costs a per-peer route the reconciler diffs forever.

The /24 remains the answer for an inbound whose clients sit outside its device
prefix, which is what a rebased or hand-edited address leaves behind: following
the device there would allocate into a range nothing on the wire is using.
*/
func wireguardAllocationBase(used []string, fallback string) string {
	configured, err := netip.ParsePrefix(fallback)
	for _, u := range used {
		a := wireguardHostAddr(u)
		if !a.IsValid() || !a.Is4() || a.IsUnspecified() {
			continue
		}
		if err == nil && configured.Contains(a) {
			return configured.Masked().String()
		}
		if p, pErr := a.Prefix(24); pErr == nil {
			return p.String()
		}
	}
	return fallback
}

const wireguardPoolFloorBits = 16

func allocateWireguardAddress(used []string, base string, blocked []netip.Prefix) (string, error) {
	if base == "" {
		base = defaultWireguardBase
	}
	prefix, err := netip.ParsePrefix(base)
	if err != nil {
		return "", err
	}
	taken := make(map[netip.Addr]struct{}, len(used))
	for _, u := range used {
		if a := wireguardHostAddr(u); a.IsValid() {
			taken[a] = struct{}{}
		}
	}
	scopes := []netip.Prefix{prefix}
	if prefix.Addr().Is4() && prefix.Bits() > wireguardPoolFloorBits {
		if wider, wErr := prefix.Addr().Prefix(wireguardPoolFloorBits); wErr == nil {
			scopes = append(scopes, wider)
		}
	}
	for _, scope := range scopes {
		addr := scope.Masked().Addr().Next().Next()
		for scope.Contains(addr) {
			// The widened scope reaches past this inbound's own subnet, so an address
			// another host device already answers for has to be stepped over.
			if _, ok := taken[addr]; !ok && !insideAny(addr, blocked) {
				return addr.String() + "/32", nil
			}
			addr = addr.Next()
		}
	}
	return "", common.NewError("wireguard: no free address available in", scopes[len(scopes)-1].String())
}

func insideAny(addr netip.Addr, blocked []netip.Prefix) bool {
	for _, p := range blocked {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// normalizeWireguardAllowedIPs validates user-supplied allowedIPs entries and
// canonicalizes them: bare addresses become single-host prefixes, duplicates drop.
func normalizeWireguardAllowedIPs(values []string) ([]string, error) {
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		p, err := netip.ParsePrefix(v)
		if err != nil {
			a, aErr := netip.ParseAddr(v)
			if aErr != nil {
				return nil, common.NewError("wireguard: invalid allowedIPs entry:", v)
			}
			p = netip.PrefixFrom(a, a.BitLen())
		}
		norm := p.String()
		if _, dup := seen[norm]; dup {
			continue
		}
		seen[norm] = struct{}{}
		out = append(out, norm)
	}
	return out, nil
}

func wireguardAllowedIPsCollision(entries, used []string) string {
	taken := make(map[string]struct{}, len(used))
	for _, u := range used {
		taken[strings.TrimSpace(u)] = struct{}{}
	}
	for _, e := range entries {
		if _, ok := taken[e]; ok {
			return e
		}
	}
	return ""
}

// defaultWireguardClients fills in blank WireGuard credentials for newly added
// clients: a generated keypair when none was provided, a derived public key when
// only a private key was given, and a unique tunnel address allocated from the
// inbound's subnet. It mutates both the typed clients and the parallel raw client
// maps that get persisted into the inbound settings. Existing values are never
// overwritten, so editing a client never rotates its keys.
func defaultWireguardClients(existing, clients []model.Client, interfaceClients []any, pool wireguardPool) error {
	own := make([]string, 0)
	owners := make(map[string]string, len(existing)+len(clients))
	for i := range existing {
		own = append(own, existing[i].AllowedIPs...)
		if key := strings.TrimSpace(existing[i].PublicKey); key != "" {
			if _, taken := owners[key]; !taken {
				owners[key] = existing[i].Email
			}
		}
	}
	// The base follows this inbound's own clients, then its own device subnet:
	// derived from the whole host set it would follow whichever inbound sorted first.
	base := wireguardAllocationBase(own, pool.base)
	used := append(slices.Clone(pool.taken), own...)
	for i := range clients {
		c := &clients[i]
		if c.PrivateKey == "" && c.PublicKey == "" {
			priv, pub, err := wgutil.GenerateWireguardKeypair()
			if err != nil {
				return err
			}
			c.PrivateKey = priv
			c.PublicKey = pub
		} else if c.PublicKey == "" && c.PrivateKey != "" {
			pub, err := wgutil.PublicKeyFromPrivate(c.PrivateKey)
			if err != nil {
				return err
			}
			c.PublicKey = pub
		}
		if other, taken := owners[strings.TrimSpace(c.PublicKey)]; taken {
			return common.NewError("wireguard: public key already used by client:", other)
		}
		owners[strings.TrimSpace(c.PublicKey)] = c.Email
		if len(c.AllowedIPs) == 0 {
			addr, err := allocateWireguardAddress(used, base, pool.blocked)
			if err != nil {
				return err
			}
			c.AllowedIPs = []string{addr}
		} else {
			normalized, err := normalizeWireguardAllowedIPs(c.AllowedIPs)
			if err != nil {
				return err
			}
			if len(normalized) == 0 {
				return common.NewError("wireguard: allowedIPs has no usable entry")
			}
			if hit := wireguardAllowedIPsCollision(normalized, used); hit != "" {
				return common.NewError("wireguard: allowedIPs entry already used by another client:", hit)
			}
			c.AllowedIPs = normalized
		}
		used = append(used, c.AllowedIPs...)

		if i < len(interfaceClients) {
			if m, ok := interfaceClients[i].(map[string]any); ok {
				m["privateKey"] = c.PrivateKey
				m["publicKey"] = c.PublicKey
				m["allowedIPs"] = c.AllowedIPs
				if c.PreSharedKey != "" {
					m["preSharedKey"] = c.PreSharedKey
				}
				if c.KeepAlive > 0 {
					m["keepAlive"] = c.KeepAlive
				}
				interfaceClients[i] = m
			}
		}
	}
	return nil
}
