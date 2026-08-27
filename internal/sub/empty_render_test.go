package sub

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/xray"
)

// A distinctive figure, so the assertion cannot pass on an incidental zero.
const unrenderableUpBytes int64 = 7_654_321

/*
A client whose link cannot be rendered must not become a blank line.

getSubs appended whatever GetLink returned, including "". subscribableProtocols
is registry-driven and excludes only "tun", so http, mixed and tunnel inbounds
reach it and render nothing — and so does a WireGuard client holding no usable
key, which genWireguardLink refuses rather than handing out a config that cannot
connect.

The result is a base64 body padded with newlines. Observed on the dev box before
this fix: subId `fresh1` returned a 4-byte body that decodes to whitespace.
Worse, the caller's own `len(subs) == 0` guard is then false — the slice holds
empty strings — so a subId whose inbounds ALL render nothing gets a 200 with a
body of newlines instead of the error page.

The guard is on the result pair only. seenEmails stays unconditional: it feeds
AggregateTrafficByEmails, so a bare `continue` would drop the client from the
Subscription-Userinfo totals, trading one bug for another. F311.
*/
func TestASubscriptionSkipsAnInboundThatRendersNothing(t *testing.T) {
	initSubDB(t)

	// A VLESS inbound renders a link; a mixed one has no shareable link at all
	// and is what used to contribute the blank line.
	seedSubInbound(t, "emptysub", "renders", 24801, 1,
		`{"network":"tcp","security":"none","tcpSettings":{"header":{"type":"none"}}}`)
	mixed := seedMixedInbound(t, "emptysub", "renders-nothing", 24802)

	// Traffic for the client whose link cannot be rendered. If it survives into
	// the aggregate, the guard kept it in seenEmails.
	if err := database.GetDB().Create(&xray.ClientTraffic{
		InboundId: mixed.Id, Email: "renders-nothing@e", Enable: true, Up: unrenderableUpBytes,
	}).Error; err != nil {
		t.Fatalf("seed traffic: %v", err)
	}

	links, emails, _, traffic, err := NewSubService("").GetSubs("emptysub", "req.example.com")
	if err != nil {
		t.Fatalf("GetSubs: %v", err)
	}

	for i, link := range links {
		if strings.TrimSpace(link) == "" {
			t.Fatalf("entry %d is blank: the body becomes base64 of newlines, and the caller's own len()==0 check stops firing because the slice is not empty. Got %#v", i, links)
		}
	}
	if len(links) != 1 {
		t.Fatalf("want the one renderable link, got %d: %#v", len(links), links)
	}
	if len(emails) != 1 {
		t.Fatalf("emails = %#v, want just the client that rendered", emails)
	}

	// The half a bare `continue` would have broken. traffic.Enable is derived
	// from hasEnabledClient and is true either way, so it proves nothing --
	// an audit caught that. The bytes are the real evidence: they can only be
	// there if the unrenderable client survived into uniqueEmails.
	if traffic.Up != unrenderableUpBytes {
		t.Fatalf("Subscription-Userinfo upload is %d, want %d: the client whose inbound rendered nothing was dropped from the traffic aggregate, which is what a bare `continue` here would do",
			traffic.Up, unrenderableUpBytes)
	}
}

// seedMixedInbound creates a mixed inbound with one client on subId. `mixed` has
// no share-link form, so GetLink returns "" for it — which is the whole point.
func seedMixedInbound(t *testing.T, subId, tag string, port int) *model.Inbound {
	t.Helper()
	db := database.GetDB()
	email := tag + "@e"
	inbound := &model.Inbound{
		UserId: 1, Tag: tag, Enable: true, Listen: "203.0.113.5", Port: port,
		Protocol: model.Mixed, Remark: tag,
		Settings: `{"accounts":[{"user":"` + email + `","pass":"p"}],"udp":true}`,
	}
	if err := db.Create(inbound).Error; err != nil {
		t.Fatalf("seed mixed inbound: %v", err)
	}
	client := &model.ClientRecord{Email: email, SubID: subId, Enable: true}
	if err := db.Create(client).Error; err != nil {
		t.Fatalf("seed client: %v", err)
	}
	if err := db.Create(&model.ClientInbound{ClientId: client.Id, InboundId: inbound.Id}).Error; err != nil {
		t.Fatalf("link client: %v", err)
	}
	return inbound
}
