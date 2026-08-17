package outbound

/*
ProbeMarked times a real request out of a socket carrying mark, filling result
exactly as a probe through a core's proxy fills it.

Deliberately the same timing code and the same Cloudflare exit trace: the point
of the marked route is that one latency column can hold both kinds of exit and
still mean one thing. The caller owns the question of whether the mark actually
reaches anywhere — by the time this runs, that has to be settled.

Mode "tcp" is promoted to the HTTP probe. A bare dial proves nothing about a
WireGuard uplink, whose peer answers no unauthenticated packet, which is the
same reason the outbound lane forces UDP protocols down this path.
*/
func ProbeMarked(mark uint32, mode string, result *TestOutboundResult) {
	result.Mode = MarkedProbeMode(mode)
	probeThroughRoute(markRoute{mark: mark, timeout: httpProbeTimeout}, defaultTestURL, httpProbeTimeout, mode == "real", result)
}

// MarkedProbeMode is the probe a marked route would actually run, which the
// caller needs before it decides to run one: a result refused up front still
// has to say which probe was asked for, and naming a tcp probe nothing performs
// is how a mode label stops meaning anything.
func MarkedProbeMode(mode string) string {
	if label := probeModeLabel(mode); label != "tcp" {
		return label
	}
	return "http"
}
