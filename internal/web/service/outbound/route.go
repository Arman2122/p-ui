package outbound

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

/*
probeRoute is how a probe's packets reach the internet.

Two answers, and the point of the seam is that the timing code cannot tell them
apart: a core's traffic leaves through a loopback SOCKS inbound the panel spun
up, while a host-level uplink is reached by marking the socket and letting the
panel's own policy rules steer it. Both must time the same request the same way,
or the latency column means two different things in one table.
*/
type probeRoute interface {
	// newTransport builds a transport whose connections leave by this route.
	// tlsConfig may be nil, which takes Go's defaults.
	newTransport(tlsConfig *tls.Config) (*http.Transport, error)
}

// socksRoute reaches an outbound through the temporary xray instance's loopback
// inbound for it.
type socksRoute struct{ port int }

func (r socksRoute) proxyURL() *url.URL {
	return &url.URL{Scheme: "socks5", Host: net.JoinHostPort("127.0.0.1", strconv.Itoa(r.port))}
}

func (r socksRoute) newTransport(tlsConfig *tls.Config) (*http.Transport, error) {
	return &http.Transport{
		Proxy:               http.ProxyURL(r.proxyURL()),
		TLSClientConfig:     tlsConfig,
		MaxIdleConns:        1,
		MaxIdleConnsPerHost: 1,
		IdleConnTimeout:     httpProbeTimeout,
	}, nil
}

/*
markRoute reaches a host-level egress by stamping the socket with that egress's
fwmark, which the panel's ip rule steers into the egress's own table.

The caller must have confirmed the rule and the table are actually installed.
An unmatched mark is not an error the kernel reports — the packet simply falls
through to the main table and leaves with the host's own address, so a probe
would time the direct path and label it the uplink's.
*/
type markRoute struct {
	mark uint32
	// network pins the dial to the families this egress carries. Left to Go, a
	// v4-only uplink is probed over its own v6 blackhole and reports a working
	// exit as "invalid argument".
	network string
	// timeout bounds the dial, since a tunnel whose peer is unreachable answers
	// nothing at all rather than refusing.
	timeout time.Duration
}

func (r markRoute) newTransport(tlsConfig *tls.Config) (*http.Transport, error) {
	control, err := markedSocketControl(r.mark)
	if err != nil {
		return nil, err
	}
	dialer := &net.Dialer{Timeout: r.timeout, Control: control}
	network := r.network
	if network == "" {
		network = "tcp"
	}
	return &http.Transport{
		DialContext: func(ctx context.Context, _, address string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, address)
		},
		TLSClientConfig:     tlsConfig,
		MaxIdleConns:        1,
		MaxIdleConnsPerHost: 1,
		IdleConnTimeout:     r.timeout,
	}, nil
}
