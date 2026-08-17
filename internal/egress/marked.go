package egress

import (
	"context"
	"fmt"
)

/*
MarkedExit names the device a socket carrying this egress's mark would actually
leave through, reading the host rather than the row.

It exists because of what a mark does when nothing catches it. The rule and the
table are separate objects: converge half of them — a row disabled, a device
gone, a provision that failed — and the marked socket silently falls through to
the main table and leaves with the host's own address. A probe measuring that
would time the server's direct path and report the number under the uplink's
name, which is worse than refusing to answer.

An empty device with a nil error is the honest "marked, but not routed anywhere"
answer: the band selects the traffic and the table contains it.
*/
func (m *Manager) MarkedExit(ctx context.Context, id int) (MarkedRoute, error) {
	if err := checkID(id); err != nil {
		return MarkedRoute{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	snap, err := m.plane.Snapshot(ctx)
	if err != nil {
		return MarkedRoute{}, err
	}

	var caught bool
	for _, rule := range snap.Rules {
		if rule.Mark == Mark(id) && rule.Table == Table(id) {
			caught = true
			break
		}
	}
	if !caught {
		return MarkedRoute{}, fmt.Errorf("%w: egress %d has no rule catching its mark, so a marked socket would leave via main", ErrNotRouted, id)
	}

	// The blackhole shares the table with the front route and outranks nothing:
	// a table holding only it is a contained egress, not a routed one.
	var routed MarkedRoute
	for _, route := range snap.Routes {
		if route.Table != Table(id) || route.Type != RouteUnicast || route.Device == "" {
			continue
		}
		routed.Device = route.Device
		switch route.Family {
		case FamilyV4:
			routed.V4 = true
		case FamilyV6:
			routed.V6 = true
		}
	}
	return routed, nil
}

/*
MarkedRoute is where a marked socket lands and which families can carry it.

The families matter as much as the device. An uplink holding only a v4 address
gets a v4 route and a v6 blackhole, so a probe left to Go's happy-eyeballs picks
the AAAA, finds no source address to select and fails with "invalid argument" —
a working v4 exit reported as broken, in the language of a syscall rather than
of the tunnel.
*/
type MarkedRoute struct {
	Device string
	V4, V6 bool
}

// Routed reports whether any family actually leaves through the device, as
// opposed to being caught by the rule and dropped inside the table.
func (r MarkedRoute) Routed() bool { return r.Device != "" && (r.V4 || r.V6) }

// Network is the dial network a probe must use to stay inside the families this
// egress carries. Empty when nothing is routed.
func (r MarkedRoute) Network() string {
	switch {
	case r.V4 && r.V6:
		return "tcp"
	case r.V4:
		return "tcp4"
	case r.V6:
		return "tcp6"
	}
	return ""
}
