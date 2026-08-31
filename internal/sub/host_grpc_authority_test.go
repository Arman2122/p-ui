package sub

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
A host's Host header reaches a gRPC inbound's link.

gRPC has no host param: under HTTP/2 the :authority pseudo-header IS the Host
header, and it is what a CDN in front of a gRPC inbound routes on. The override
only replaced keys the transport already emitted, and gRPC emits serviceName and
authority -- so a Host header set on a gRPC host row was stored, shown in the
form, and dropped from every link it was supposed to shape. F147.
*/
func TestAHostsHeaderReachesAGrpcLinkAsAuthority(t *testing.T) {
	seedSubDB(t)
	ib := seedSubInbound(t, "g1", "grpc-in", 45411, 0,
		`{"network":"grpc","security":"tls","grpcSettings":{"serviceName":"svc","authority":"origin.example.com"}}`)
	seedHost(t, &model.Host{
		InboundId: ib.Id, SortOrder: 1, Remark: "CDN", Address: "cdn.example.com",
		Port: 8443, Security: "tls", HostHeader: "front.example.com",
	})

	links, _, _, _, err := NewSubService("").GetSubs("g1", "req.example.com")
	if err != nil {
		t.Fatalf("GetSubs: %v", err)
	}
	joined := strings.Join(links, "\n")
	if !strings.Contains(joined, "authority=front.example.com") {
		t.Fatalf("the host's Host header never reached the link; a CDN in front of this inbound routes on the origin's authority instead:\n%s", joined)
	}
	if strings.Contains(joined, "authority=origin.example.com") {
		t.Fatalf("the inbound's own authority survived the host override:\n%s", joined)
	}
}

// And the transports that DO have a host param are unchanged.
func TestAHostsHeaderStillReachesAWebsocketLink(t *testing.T) {
	seedSubDB(t)
	ib := seedSubInbound(t, "w1", "ws-in", 45412, 0,
		`{"network":"ws","security":"tls","wsSettings":{"path":"/p","host":"origin.example.com"}}`)
	seedHost(t, &model.Host{
		InboundId: ib.Id, SortOrder: 1, Remark: "CDN", Address: "cdn.example.com",
		Port: 8443, Security: "tls", HostHeader: "front.example.com",
	})

	links, _, _, _, err := NewSubService("").GetSubs("w1", "req.example.com")
	if err != nil {
		t.Fatalf("GetSubs: %v", err)
	}
	joined := strings.Join(links, "\n")
	if !strings.Contains(joined, "host=front.example.com") {
		t.Fatalf("the host's Host header no longer reaches a websocket link:\n%s", joined)
	}
	if strings.Contains(joined, "authority=") {
		t.Fatalf("a websocket link grew an authority param it never had:\n%s", joined)
	}
}
