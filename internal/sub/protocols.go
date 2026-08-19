package sub

import (
	"slices"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
Which protocols a subscription can carry, asked of the core registry rather than
listed here.

The list used to be a literal, in the SQL that selects a subscriber's inbounds
and again at every place that renders one. Adding AmneziaWG to the panel and not
to those literals produced exactly the failure this file exists to stop: the
inbound was created, the client was attached, the device came up serving them,
and the subscription page was EMPTY -- the query filtered the inbound out before
anything could render it, with no error anywhere.

A core added later is carried by all of this the moment it declares itself.
*/

// subscribableProtocols are the kinds a subscription may list. A kind serves a
// subscription when some core in this build claims it.
func subscribableProtocols() []string {
	kinds := cores.Kinds()
	out := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		// tun is a local device rather than something a client subscribes to.
		if kind == "tun" {
			continue
		}
		out = append(out, string(kind))
	}
	return out
}

// carriesWireguardClient reports a kind whose clients are configured by a
// WireGuard keypair and tunnel address, whichever core serves it. Kernel
// WireGuard, AmneziaWG and Xray's userspace tunnel all answer yes.
func carriesWireguardClient(protocol model.Protocol) bool {
	return slices.Contains(cores.ClientCredentials(core.Kind(protocol)), core.CredAllowedIPs)
}
