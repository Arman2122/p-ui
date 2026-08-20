package outbound

import "time"

/*
probeTargets are tried in order until one answers.

One destination makes the probe a test of that destination as much as of the
exit. Measured on the test box: a working Surfshark uplink reported a 10s
timeout because the single address DNS returned was unreachable through it, and
three seconds later the same uplink answered in 500ms. An operator reads that as
a broken exit and starts debugging a tunnel that was never down.

Two independent networks, so a destination-specific block or a bad anycast POP
has to happen twice before an exit is called dead.
*/
var probeTargets = []string{
	defaultTestURL,
	"https://cp.cloudflare.com/generate_204",
}

/*
ProbeMarked times a real request out of a socket carrying mark, filling result
exactly as a probe through a core's proxy fills it.

Deliberately the same timing code and the same Cloudflare exit trace: the point
of the marked route is that one latency column can hold both kinds of exit and
still mean one thing. The caller owns whether the mark actually reaches
anywhere -- by the time this runs, that has to be settled.

Mode "tcp" is promoted to the HTTP probe. A bare dial proves nothing about a
WireGuard uplink, whose peer answers no unauthenticated packet, which is the
same reason the outbound lane forces UDP protocols down this path.
*/
func ProbeMarked(mark uint32, network, mode string, result *TestOutboundResult) {
	probeMarkedTargets(probeThroughRoute, probeTargets, mark, network, mode, result)
}

// probeMarkedTargets is ProbeMarked with the prober and the targets injected, so
// the fallback is testable without a network.
func probeMarkedTargets(
	probe func(probeRoute, string, time.Duration, bool, *TestOutboundResult),
	targets []string,
	mark uint32,
	network, mode string,
	result *TestOutboundResult,
) {
	label := MarkedProbeMode(mode)
	route := markRoute{mark: mark, network: network, timeout: httpProbeTimeout}

	for i, target := range targets {
		attempt := TestOutboundResult{Tag: result.Tag, Mode: label}
		probe(route, target, httpProbeTimeout, mode == "real", &attempt)
		// The last attempt's answer stands either way: if every destination
		// failed, the exit really is unreachable and the operator needs the error.
		if attempt.Success || i == len(targets)-1 {
			*result = attempt
			return
		}
	}
}

// MarkedProbeMode is the probe a marked route would actually run, which the
// caller needs before it decides to run one: a result refused up front still has
// to say which probe was asked for, and naming a tcp probe nothing performs is
// how a mode label stops meaning anything.
func MarkedProbeMode(mode string) string {
	if label := probeModeLabel(mode); label != "tcp" {
		return label
	}
	return "http"
}
