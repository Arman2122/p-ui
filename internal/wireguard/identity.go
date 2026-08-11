package wireguard

import (
	"context"
	"fmt"
	"net/netip"
	"time"
)

// Endpoint is one client's live tunnel as the kernel sees it: the outer address
// its last handshake came from, and when that was.
type Endpoint struct {
	Email     string
	Source    netip.Addr
	Handshake time.Time
}

// Endpoints names every client with a live tunnel, across every device. It only
// reads: a traffic scrape would advance the counters and discard that delta.
func (m *Manager) Endpoints(ctx context.Context) []Endpoint {
	m.mu.Lock()
	ids := make([]int, 0, len(m.devices))
	for id := range m.devices {
		ids = append(ids, id)
	}
	m.mu.Unlock()

	var out []Endpoint
	for _, id := range ids {
		out = append(out, m.deviceEndpoints(ctx, id)...)
	}
	return out
}

// deviceEndpoints reads one device. A peer with no endpoint has never been on
// the wire, and one outside the handshake window is a tunnel already gone.
func (m *Manager) deviceEndpoints(ctx context.Context, id int) []Endpoint {
	m.mu.Lock()
	defer m.mu.Unlock()

	rec, ok := m.devices[id]
	if !ok {
		return nil
	}
	snap, err := m.plane.Snapshot(ctx, InterfaceName(id))
	if err != nil || !snap.Exists {
		return nil
	}
	var out []Endpoint
	for _, peer := range snap.Device.Peers {
		email, known := rec.byKey[peer.PublicKey]
		if !known || peer.Endpoint == nil || !isOnline(peer.LastHandshakeTime) {
			continue
		}
		source, ok := netip.AddrFromSlice(peer.Endpoint.IP)
		if !ok {
			continue
		}
		out = append(out, Endpoint{Email: email, Source: source.Unmap(), Handshake: peer.LastHandshakeTime})
	}
	return out
}

/*
PeerAllowedIPs maps each client on one device to the prefixes the KERNEL holds
for it, rather than the ones the panel last pushed.

The kernel is the authority here: a prefix claimed by two peers is MOVED to the
later one, so a caller keying on the panel's own view keys on the wrong client.
An absent device is ErrNoDevice, which is a state the caller waits out.
*/
func (m *Manager) PeerAllowedIPs(ctx context.Context, id int) (map[string][]netip.Prefix, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	rec, ok := m.devices[id]
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoDevice, InterfaceName(id))
	}
	snap, err := m.plane.Snapshot(ctx, InterfaceName(id))
	if err != nil {
		return nil, m.note(err)
	}
	if !snap.Exists {
		return nil, fmt.Errorf("%w: %s", ErrNoDevice, InterfaceName(id))
	}
	out := make(map[string][]netip.Prefix, len(snap.Device.Peers))
	for _, peer := range snap.Device.Peers {
		email, known := rec.byKey[peer.PublicKey]
		if !known {
			continue
		}
		for i := range peer.AllowedIPs {
			if prefix, ok := toPrefix(&peer.AllowedIPs[i]); ok {
				out[email] = append(out[email], prefix)
			}
		}
	}
	return out, nil
}
