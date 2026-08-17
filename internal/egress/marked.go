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
func (m *Manager) MarkedExit(ctx context.Context, id int) (string, error) {
	if err := checkID(id); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	snap, err := m.plane.Snapshot(ctx)
	if err != nil {
		return "", err
	}

	var caught bool
	for _, rule := range snap.Rules {
		if rule.Mark == Mark(id) && rule.Table == Table(id) {
			caught = true
			break
		}
	}
	if !caught {
		return "", fmt.Errorf("%w: egress %d has no rule catching its mark, so a marked socket would leave via main", ErrNotRouted, id)
	}

	// The blackhole shares the table with the front route and outranks nothing:
	// a table holding only it is a contained egress, not a routed one.
	for _, route := range snap.Routes {
		if route.Table == Table(id) && route.Type == RouteUnicast && route.Device != "" {
			return route.Device, nil
		}
	}
	return "", nil
}
