package sub

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
The panel's own per-client link endpoint must render the same hosts a
subscriber gets.

LinksForClient called GetLink directly, with no hostEndpoints lookup, so
GET /panel/api/clients/links/:email handed out the origin address on exactly
the deployments hosts exist for — a CDN-fronted inbound whose origin port is
closed produced a link that cannot connect, from the same page that shows the
hosts. The subscription rendered it correctly at the same moment.
*/
func TestLinksForClient_RendersTheInboundsHosts(t *testing.T) {
	seedSubDB(t)
	ib := seedSubInbound(t, "s1", "hosted", 4451, 1, wsTLSStream)
	seedHost(t, &model.Host{
		InboundId: ib.Id, SortOrder: 1, Remark: "A", Address: "a.cdn.com",
		Port: 2096, Security: "tls", Sni: "a.sni", HostHeader: "a.host", Path: "/a",
	})

	links := NewLinkProvider().LinksForClient("req.example.com", ib, "hosted@e")
	if len(links) != 1 {
		t.Fatalf("links = %d, want 1: %v", len(links), links)
	}
	if !strings.Contains(links[0], "a.cdn.com:2096") {
		t.Fatalf("per-client link does not name the host: %s", links[0])
	}
	if strings.Contains(links[0], "203.0.113.5") {
		t.Fatalf("per-client link leaks the origin address the host exists to hide: %s", links[0])
	}
}

// Two hosts fan out per client exactly as they do for a subscriber.
func TestLinksForClient_FansOutOverEveryHost(t *testing.T) {
	seedSubDB(t)
	ib := seedSubInbound(t, "s1", "multi", 4452, 1, wsTLSStream)
	seedHost(t, &model.Host{InboundId: ib.Id, SortOrder: 2, Remark: "B", Address: "b.cdn.com", Port: 8443, Security: "tls", Sni: "b.sni", HostHeader: "b.host", Path: "/b"})
	seedHost(t, &model.Host{InboundId: ib.Id, SortOrder: 1, Remark: "A", Address: "a.cdn.com", Port: 2096, Security: "tls", Sni: "a.sni", HostHeader: "a.host", Path: "/a"})

	links := NewLinkProvider().LinksForClient("req.example.com", ib, "multi@e")
	if len(links) != 2 {
		t.Fatalf("links = %d, want 2: %v", len(links), links)
	}
	if !strings.Contains(links[0], "a.cdn.com:2096") || !strings.Contains(links[1], "b.cdn.com:8443") {
		t.Fatalf("hosts not rendered in sort order: %v", links)
	}
}

// An inbound with no hosts renders exactly what it rendered before: the origin.
func TestLinksForClient_NoHostsKeepsTheOrigin(t *testing.T) {
	seedSubDB(t)
	ib := seedSubInbound(t, "s1", "bare", 4453, 1, wsTLSStream)

	links := NewLinkProvider().LinksForClient("req.example.com", ib, "bare@e")
	if len(links) != 1 {
		t.Fatalf("links = %d, want 1: %v", len(links), links)
	}
	if !strings.Contains(links[0], "203.0.113.5:4453") {
		t.Fatalf("no-hosts link should be the inbound's own address: %s", links[0])
	}
}
