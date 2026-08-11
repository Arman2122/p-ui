package egress

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
)

// The two host globals an egress depends on and cannot own. Effective reverse path
// filtering is max(all, dev), so the panel must refuse rather than pretend.
// Both families are named because the .conf the panel hands a client routes
// ::/0 into the tunnel, so a v4-only host drops every v6 flow silently.
const (
	AllRPFilterKey = "net.ipv4.conf.all.rp_filter"
	IPForwardKey   = "net.ipv4.ip_forward"
	IPForward6Key  = "net.ipv6.conf.all.forwarding"
)

// Report is what preflight found. A refusal blocks an attach; a note is a host
// fact the operator has to know about but the panel does not own.
type Report struct {
	Refusals []error
	Notes    []string
}

// Err joins the refusals so a caller can still match the sentinel that names the
// remedy — errors.Is over a flattened string would lose exactly that.
func (r Report) Err() error { return errors.Join(r.Refusals...) }

func (r Report) OK() bool { return len(r.Refusals) == 0 }

/*
Preflight makes §5.3's asserts real: the reserved band is the panel's alone, and
the return path the front depends on is not filtered away.

It is deliberately not part of the reconcile loop. Drift repair runs on a tick
and would either shout the same refusal forever or start deleting objects it has
no claim to; preflight instead answers once, at attach and whenever an operator
asks, naming the exact resource so there is something to act on.
*/
func (m *Manager) Preflight(ctx context.Context, gatewayBase netip.Prefix, rows ...Egress) Report {
	var report Report
	if err := m.probeHost(ctx); err != nil {
		report.Refusals = append(report.Refusals, err)
		return report
	}
	if err := CheckGatewayBase(gatewayBase); err != nil {
		report.Refusals = append(report.Refusals, err)
	}

	snap, err := m.plane.Snapshot(ctx)
	if err != nil {
		report.Refusals = append(report.Refusals, err)
		return report
	}
	report.Refusals = append(report.Refusals, foreignBandObjects(snap)...)

	// A gateway /32 that duplicates an address already on the box would answer
	// for its return traffic, and the collision is invisible from inside Xray.
	if gatewayBase.IsValid() {
		for _, addr := range snap.Addrs {
			if !gatewayBase.Overlaps(addr.Prefix) || ownGateway(gatewayBase, addr) {
				continue
			}
			report.Refusals = append(report.Refusals, fmt.Errorf(
				"%w: %s already carries %s — move the egress gateway base off it",
				ErrGatewayBase, gatewayBase, addr))
		}
	}

	if value, err := m.plane.Sysctl(ctx, AllRPFilterKey); err != nil {
		report.Notes = append(report.Notes, fmt.Sprintf("%s could not be read (%v); a strict value silently kills the front's return path", AllRPFilterKey, err))
	} else if value == "1" {
		report.Refusals = append(report.Refusals, fmt.Errorf(
			"%w: %s is 1 and the effective value is max(all, dev), so the panel cannot lower it per device — set it to 0 or 2",
			ErrStrictRPFilter, AllRPFilterKey))
	}

	// Forwarding is a precondition of any L3 core that reaches the internet, so
	// it is reported and never owned: turning it on is not this feature's call.
	for _, key := range []string{IPForwardKey, IPForward6Key} {
		if value, err := m.plane.Sysctl(ctx, key); err == nil && value == "0" {
			report.Notes = append(report.Notes, fmt.Sprintf(
				"%s is 0, so no L3 inbound on this host forwards a packet of that family at all, egress or not", key))
		}
	}
	report.Notes = append(report.Notes, m.darkFronts(snap, rows)...)
	return report
}

/*
darkFronts names every enabled row whose front is not on this host.

It is a note and never a refusal: the front belongs to the core and appears after
its process does, so an absent one is the normal state between a restart and the
next reconcile. It is worth saying because nothing else says it — the row reads as
enabled, Selects still answers "routed", and the egress is contained, i.e. dark.
*/
func (m *Manager) darkFronts(snap Snapshot, rows []Egress) []string {
	var notes []string
	for _, row := range rows {
		driver, known := m.drivers.For(row.Type)
		if !row.Enable || !known {
			continue
		}
		fill, err := driver.Fill(row)
		if err != nil || fill.Device == "" || frontIsUp(fill, snap) {
			continue
		}
		notes = append(notes, fmt.Sprintf(
			"egress %d has no front on this host yet: %s is absent or is not carrying %s, so everything attached to it is contained rather than routed",
			row.ID, fill.Device, fill.Addr))
	}
	return notes
}

/*
ownGateway reports whether addr is a front carrying exactly the address its own
id derives — which every enabled xray-tun egress puts on peg<id> by design, so
without this every healthy host refuses itself.

Both halves are required. Matching by value alone would exempt a squatter that
happens to hold 100.127.0.1/32 on eth0; matching by device alone would exempt
anything else a front picked up.
*/
func ownGateway(base netip.Prefix, addr AddrSpec) bool {
	id, mine := ownedEgressID(addr.Device)
	if !mine {
		return false
	}
	gateway, err := Gateway(base, id)
	return err == nil && gateway == addr.Prefix
}

/*
foreignBandObjects names everything in the reserved band the panel cannot claim.

The wg-quick (51820+) and sing-box (2022) defaults §5.3 asks about are outside
30001-30999 and 31001-31999 entirely, so this one walk subsumes that check: a
collision from either tool would have to appear here to matter.
*/
func foreignBandObjects(snap Snapshot) []error {
	var found []error
	for _, rule := range snap.Rules {
		id, mine := prioEgressID(rule.Priority)
		if mine && rule.Table == Table(id) {
			continue
		}
		found = append(found, fmt.Errorf("%w: %s sits in the reserved priority band — move it out of %d..%d",
			ErrForeignResource, rule, prioBase+MinID, prioBase+MaxID))
	}
	for _, route := range snap.Routes {
		if _, mine := tableEgressID(route.Table); !mine {
			continue
		}
		// Ownership here is what an id names for itself, because preflight holds no
		// rows. A driver that names its own device must be consulted from here.
		if route.Dst == route.Family.DefaultRoute() {
			if route.Type == RouteBlackhole {
				continue
			}
			if route.Type == RouteUnicast && ownsDevice("", route.Device) {
				continue
			}
		}
		found = append(found, fmt.Errorf("%w: %s sits in a reserved table — move it out of %d..%d",
			ErrForeignResource, route, tableBase+MinID, tableBase+MaxID))
	}
	return found
}
