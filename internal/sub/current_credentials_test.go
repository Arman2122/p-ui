package sub

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	wgutil "github.com/Arman2122/p-ui/internal/util/wireguard"
)

func TestDirectAndAllLinksUseCurrentCredential(t *testing.T) {
	initSubDB(t)
	inbound := seedSubInbound(t, "current-sub", "current-link", 24813, 1, `{"network":"tcp","security":"none"}`)
	const currentUUID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
	if err := database.GetDB().Model(&model.ClientRecord{}).Where("email = ?", "current-link@e").Update("uuid", currentUUID).Error; err != nil {
		t.Fatal(err)
	}
	seedHost(t, &model.Host{InboundId: inbound.Id, Address: "managed.example.com", Port: 8443, Security: "none"})
	for _, path := range []string{"direct", "hosted", "all"} {
		t.Run(path, func(t *testing.T) {
			svc := NewSubService("")
			var links []string
			switch path {
			case "direct":
				links = []string{svc.GetLink(inbound, "current-link@e")}
			case "hosted":
				links = []string{svc.linkForEmail(inbound, "current-link@e")}
			case "all":
				links = svc.inboundLinks(inbound)
			}
			if len(links) != 1 || !strings.HasPrefix(links[0], "vless://"+currentUUID+"@") {
				t.Fatalf("stale credential in %s links: %v", path, links)
			}
			if path != "direct" {
				parsed, err := url.Parse(links[0])
				if err != nil || parsed.Host != "managed.example.com:8443" {
					t.Fatalf("managed host missing from %s link: %s (error %v)", path, links[0], err)
				}
			}
		})
	}
}

func TestSubscriptionKeepsEachInboundTunnelIdentity(t *testing.T) {
	initSubDB(t)
	db := database.GetDB()
	key := func() string {
		t.Helper()
		private, _, err := wgutil.GenerateWireguardKeypair()
		if err != nil {
			t.Fatal(err)
		}
		return private
	}
	serverKey := key()
	shared := &model.ClientRecord{Email: "tunnel@e", SubID: "tunnel-sub", Enable: true, PrivateKey: key(), AllowedIPs: "10.99.0.2/32"}
	if err := db.Create(shared).Error; err != nil {
		t.Fatal(err)
	}
	expected := map[string]struct{ private, address string }{}
	for i := 1; i <= 2; i++ {
		host, private, address := fmt.Sprintf("203.0.113.%d", i), key(), fmt.Sprintf("10.%d.0.2/32", i)
		settings := fmt.Sprintf(`{"secretKey":%q,"clients":[{"email":"tunnel@e","enable":true,"subId":"tunnel-sub","privateKey":%q,"allowedIPs":[%q],"keepAlive":%d}]}`, serverKey, private, address, i)
		inbound := &model.Inbound{UserId: 1, Tag: fmt.Sprintf("tunnel-%d", i), Enable: true, Listen: host, Port: 51820, Protocol: model.WireGuard, Settings: settings, StreamSettings: `{}`}
		if err := db.Create(inbound).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&model.ClientInbound{ClientId: shared.Id, InboundId: inbound.Id}).Error; err != nil {
			t.Fatal(err)
		}
		expected[host] = struct{ private, address string }{private, address}
	}
	links, _, _, _, err := NewSubService("").GetSubs("tunnel-sub", "sub.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(links) != 2 {
		t.Fatalf("links=%v, want two tunnels", links)
	}
	for _, raw := range links {
		parsed, err := url.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		want, ok := expected[parsed.Hostname()]
		if !ok || parsed.User.Username() != want.private || parsed.Query().Get("address") != want.address {
			t.Fatalf("cross-inbound tunnel identity leaked: %s", raw)
		}
	}
}
