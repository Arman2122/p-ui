package sub

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/service"
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

/*
The panel's per-inbound endpoint answers both of its shapes from the same
renderer.

GET /panel/api/inbounds/:id/links is what the QR, info and Export surfaces call
now that the browser no longer builds links itself. Without ?email= it must
cover every client on the inbound; with it, exactly one. Both go through the
host-aware path — the whole point of moving the render off the browser.
*/
func TestGetInboundLinks_ScopesToTheEmailWhenGiven(t *testing.T) {
	seedSubDB(t)
	service.RegisterSubLinkProvider(NewLinkProvider())
	t.Cleanup(func() { service.RegisterSubLinkProvider(nil) })

	db := database.GetDB()
	settings := `{"clients":[` +
		`{"id":"11111111-2222-4333-8444-000000000001","email":"ann@e","enable":true},` +
		`{"id":"11111111-2222-4333-8444-000000000002","email":"bob@e","enable":true}],"decryption":"none"}`
	ib := &model.Inbound{
		UserId: 1, Tag: "pair", Enable: true, Listen: "203.0.113.5", Port: 4454,
		Protocol: model.VLESS, Remark: "pair", Settings: settings, StreamSettings: wsTLSStream,
	}
	if err := db.Create(ib).Error; err != nil {
		t.Fatalf("seed inbound: %v", err)
	}
	seedHost(t, &model.Host{InboundId: ib.Id, SortOrder: 1, Remark: "A", Address: "a.cdn.com", Port: 2096, Security: "tls", Sni: "a.sni", HostHeader: "a.host", Path: "/a"})

	svc := &service.InboundService{}
	all, err := svc.GetInboundLinks("req.example.com", ib.Id, "")
	if err != nil {
		t.Fatalf("GetInboundLinks(all): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("whole-inbound links = %d, want 2: %v", len(all), all)
	}

	one, err := svc.GetInboundLinks("req.example.com", ib.Id, "bob@e")
	if err != nil {
		t.Fatalf("GetInboundLinks(bob@e): %v", err)
	}
	if len(one) != 1 {
		t.Fatalf("per-client links = %d, want 1: %v", len(one), one)
	}
	if !strings.Contains(one[0], "a.cdn.com:2096") {
		t.Fatalf("per-client link is not host-rendered: %s", one[0])
	}
	if strings.Contains(one[0], "ann") {
		t.Fatalf("?email= did not scope the answer: %s", one[0])
	}
}
