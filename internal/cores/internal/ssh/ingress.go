package ssh

import (
	"context"

	"github.com/Arman2122/p-ui/internal/core"
)

/*
An SSH inbound is routable when, and only when, its forwards are dialled
through the panel's loopback bridge into Xray's router.

With the switch off the server dials destinations itself and Xray never sees a
packet, so a rule naming this inbound could not match whatever it said. The
editor is told that, rather than being offered a tag that routes nothing.
*/
func (c *Core) IngressSelector(kind core.Kind) core.IngressSelector {
	if kind != Kind {
		return core.IngressNone
	}
	return core.IngressInternal
}

func (c *Core) IngressHandle(_ context.Context, inst core.Instance) (core.IngressHandle, error) {
	settings, err := parseSettings(inst.Settings)
	if err != nil || !settings.RouteThroughXray || settings.RouteXrayPort <= 0 {
		return core.IngressHandle{BlockedKey: blockedBridgeOff}, nil
	}
	return core.IngressHandle{Tag: inst.Tag}, nil
}

// blockedBridgeOff is the reason the routing editor renders. The wording is
// core-agnostic and shipped, so it is shared rather than duplicated.
const blockedBridgeOff = "pages.xray.subjects.reasonBridgeOff"

/*
IngressCriteria is what a rule on this inbound can actually match.

Address, port and network come from the forward request itself. Domain is real
here and not merely allowed: a client running `ssh -D` sends a hostname, which
arrives as an SOCKS address type the bridge preserves, so the sniffer the
compiler attaches has a name to read.

Source and source port are deliberately absent. Xray stamps a session's source
from the TCP peer, and this bridge's peer is always the panel on 127.0.0.1, so
a rule written on either would save and never match. User is absent for the
same reason: the bridge authenticates nobody and the dialer is handed an
address and nothing else.
*/
func (c *Core) IngressCriteria(kind core.Kind) []core.Criterion {
	if kind != Kind {
		return nil
	}
	return []core.Criterion{core.CriterionIP, core.CriterionPort, core.CriterionNetwork, core.CriterionDomain}
}
