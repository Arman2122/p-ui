package sub

import (
	"net/url"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
A Host row's certificate pin has to be hex on EVERY protocol, not just Hysteria.

Ledger L21 closed on the inbound-level reader, pinnedSha256List, and missed this
one entirely: externalProxyPins serves the Host path, and three of its four call
sites wrote the stored value verbatim. So the exact sentence L21 said it had
removed -- a base64 paste that works on Hysteria and produces a config the core
refuses everywhere else -- stayed true for every Host-fronted inbound.

Worse, a test comment described that divergence as intended, which is how it
survived a closure claim written the same day.

Normalised in the reader now, for the same reason the inbound one is: a pin
crosses into a link at four sites, and normalising at each of them is how the
divergence happened.
*/

const hostPinBase64 = "yEfdI5XQl4wHgLggHEsomosoFZfUfCdfLXfT+W2N6cQ="
const hostPinHex = "c847dd2395d0978c0780b8201c4b289a8b281597d47c275f2d77d3f96d8de9c4"

func hostPinInbound(proto model.Protocol, settings, stream string) *model.Inbound {
	return &model.Inbound{
		Id: 1, Tag: "t", Enable: true, Listen: "203.0.113.30", Port: 443,
		Protocol: proto, Remark: "R",
		Settings:       settings,
		StreamSettings: stream,
	}
}

func TestAHostsPinIsHexOnEveryProtocol(t *testing.T) {
	host := `{"isHost":true,"forceTls":"same","dest":"cdn.example","port":443,"remark":"H",` +
		`"pinnedPeerCertSha256":["` + hostPinBase64 + `"]}`
	tlsStream := `{"network":"tcp","security":"tls",` +
		`"tlsSettings":{"serverName":"origin.example","settings":{"fingerprint":"chrome"}},` +
		`"externalProxy":[` + host + `]}`

	cases := []struct {
		name     string
		inbound  *model.Inbound
		param    string
		vmessObj bool
	}{
		{
			name: "vless carries it as pcs",
			inbound: hostPinInbound(model.VLESS,
				`{"clients":[{"id":"11111111-2222-4333-8444-555555555555","email":"u@x"}],"decryption":"none"}`,
				tlsStream),
			param: "pcs",
		},
		{
			name: "trojan carries it as pcs",
			inbound: hostPinInbound(model.Trojan,
				`{"clients":[{"password":"pw","email":"u@x"}]}`,
				tlsStream),
			param: "pcs",
		},
		{
			name: "hysteria carries it as pinSHA256, as it always did",
			inbound: hostPinInbound(model.Hysteria,
				`{"clients":[{"email":"u@x","auth":"pw"}],"version":2}`,
				`{"network":"quic","security":"tls",`+
					`"tlsSettings":{"serverName":"origin.example","settings":{"fingerprint":"chrome"}},`+
					`"externalProxy":[`+host+`]}`),
			param: "pinSHA256",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			link := (&SubService{}).GetLink(c.inbound, "u@x")
			if link == "" {
				t.Fatal("rendered no link at all")
			}
			u, err := url.Parse(strings.Split(link, "\n")[0])
			if err != nil {
				t.Fatalf("link does not parse: %v\n%s", err, link)
			}
			got := u.Query().Get(c.param)
			if got == hostPinBase64 {
				t.Fatalf("%s is the stored base64 value; the core parses this field as hex only and refuses the whole config", c.param)
			}
			if got != hostPinHex {
				t.Fatalf("%s = %q, want the hex form %q", c.param, got, hostPinHex)
			}
		})
	}
}

// And a value that is neither hex nor a 32-byte base64 still passes through, so
// the core reports the operator's typo rather than the panel swallowing it.
func TestAnUnrecognisedHostPinIsNotRewritten(t *testing.T) {
	host := `{"isHost":true,"forceTls":"same","dest":"cdn.example","port":443,"remark":"H",` +
		`"pinnedPeerCertSha256":["not-a-pin"]}`
	ib := hostPinInbound(model.VLESS,
		`{"clients":[{"id":"11111111-2222-4333-8444-555555555555","email":"u@x"}],"decryption":"none"}`,
		`{"network":"tcp","security":"tls",`+
			`"tlsSettings":{"serverName":"origin.example","settings":{"fingerprint":"chrome"}},`+
			`"externalProxy":[`+host+`]}`)

	u, err := url.Parse(strings.Split((&SubService{}).GetLink(ib, "u@x"), "\n")[0])
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got := u.Query().Get("pcs"); got != "not-a-pin" {
		t.Fatalf("pcs = %q, want the stored value untouched", got)
	}
}
