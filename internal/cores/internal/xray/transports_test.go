package xray

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

/*
An XHTTP inbound whose TLS offers h3 alone is served by Xray on QUIC, with no TCP
listener at all. Answering TCP for it let a Hysteria or kcp row share the port —
both bind UDP, Xray refuses to start, every inbound down — and refused a TCP
sibling that would have bound beside it.
*/
func TestXHTTPOverHTTP3BindsUDP(t *testing.T) {
	c := New(Deps{})
	for _, tt := range []struct {
		name   string
		stream string
		want   core.Transports
	}{
		{"xhttp h3 only", `{"network":"xhttp","security":"tls","tlsSettings":{"alpn":["h3"]}}`, core.Transports{UDP: true}},
		{"splithttp h3 only", `{"network":"splithttp","security":"tls","tlsSettings":{"alpn":["h3"]}}`, core.Transports{UDP: true}},
		{"xhttp h2 and h3", `{"network":"xhttp","security":"tls","tlsSettings":{"alpn":["h2","h3"]}}`, core.Transports{TCP: true}},
		{"xhttp default alpn", `{"network":"xhttp","security":"tls","tlsSettings":{}}`, core.Transports{TCP: true}},
		{"xhttp reality", `{"network":"xhttp","security":"reality","tlsSettings":{"alpn":["h3"]}}`, core.Transports{TCP: true}},
		{"xhttp plain", `{"network":"xhttp","security":"none"}`, core.Transports{TCP: true}},
		{"kcp", `{"network":"kcp"}`, core.Transports{UDP: true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := c.Transports("vless", `{"clients":[]}`, tt.stream); got != tt.want {
				t.Fatalf("Transports = %+v, want %+v", got, tt.want)
			}
		})
	}
}
