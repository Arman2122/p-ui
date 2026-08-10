package sub

import (
	"encoding/json"
	"net/url"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	wgutil "github.com/Arman2122/p-ui/internal/util/wireguard"
)

func TestGenWireguardLinkFields(t *testing.T) {
	serverPriv, serverPub, err := wgutil.GenerateWireguardKeypair()
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	clientPriv, _, err := wgutil.GenerateWireguardKeypair()
	if err != nil {
		t.Fatalf("client keypair: %v", err)
	}

	inbound := &model.Inbound{
		Listen:   "203.0.113.7",
		Port:     51820,
		Protocol: model.WireGuard,
		Remark:   "wg-sub",
		Settings: `{"secretKey":"` + serverPriv + `","mtu":1420,"clients":[{"email":"user","privateKey":"` + clientPriv + `","allowedIPs":["10.0.0.2/32"],"keepAlive":25}]}`,
	}

	s := &SubService{}
	link := s.genWireguardLink(inbound, "user")

	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("link does not parse: %v\n got: %s", err, link)
	}
	if u.Scheme != "wireguard" {
		t.Fatalf("scheme = %q, want wireguard", u.Scheme)
	}
	if u.Host != "203.0.113.7:51820" {
		t.Fatalf("host = %q, want 203.0.113.7:51820", u.Host)
	}
	if u.User.Username() != clientPriv {
		t.Fatalf("userinfo = %q, want client private key %q", u.User.Username(), clientPriv)
	}
	q := u.Query()
	if q.Get("publickey") != serverPub {
		t.Fatalf("publickey = %q, want server public key %q", q.Get("publickey"), serverPub)
	}
	if q.Get("address") != "10.0.0.2/32" {
		t.Fatalf("address = %q, want 10.0.0.2/32", q.Get("address"))
	}
	if q.Get("mtu") != "1420" {
		t.Fatalf("mtu = %q, want 1420", q.Get("mtu"))
	}
}

func TestGenWireguardLinkMultiAllowedIPs(t *testing.T) {
	serverPriv, _, err := wgutil.GenerateWireguardKeypair()
	if err != nil {
		t.Fatalf("keypair: %v", err)
	}
	clientPriv, _, err := wgutil.GenerateWireguardKeypair()
	if err != nil {
		t.Fatalf("client keypair: %v", err)
	}

	inbound := &model.Inbound{
		Listen:   "203.0.113.7",
		Port:     51820,
		Protocol: model.WireGuard,
		Remark:   "wg-sub",
		Settings: `{"secretKey":"` + serverPriv + `","clients":[{"email":"user","privateKey":"` + clientPriv + `","allowedIPs":["10.0.0.2/32","fd00::2/128"]}]}`,
	}

	s := &SubService{}
	link := s.genWireguardLink(inbound, "user")

	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("link does not parse: %v\n got: %s", err, link)
	}
	if got, want := u.Query().Get("address"), "10.0.0.2/32,fd00::2/128"; got != want {
		t.Fatalf("address = %q, want %q (all allowed IPs joined, not just the first)", got, want)
	}
}

func TestGenWireguardLinkWrongProtocol(t *testing.T) {
	s := &SubService{}
	vless := &model.Inbound{Protocol: model.VLESS, Settings: `{"clients":[{"email":"user"}]}`}
	if got := s.genWireguardLink(vless, "user"); got != "" {
		t.Fatalf("wrong protocol should yield empty link, got %q", got)
	}
}

func TestGenWireguardLinkNoKey(t *testing.T) {
	s := &SubService{}
	inbound := &model.Inbound{
		Protocol: model.WireGuard,
		Port:     51820,
		Settings: `{"secretKey":"x","clients":[{"email":"user"}]}`,
	}
	if got := s.genWireguardLink(inbound, "user"); got != "" {
		t.Fatalf("client without private key should yield empty link, got %q", got)
	}
}

/*
For kernel WireGuard the client config IS the product, so a format that skips
the kind hands the subscriber nothing at all. The json outbound is also pinned
to xray's own "wireguard" name — xray has no outbound called "wgkernel".
*/
func TestWgkernelIsServedByEverySubscriptionFormat(t *testing.T) {
	serverPriv, serverPub, err := wgutil.GenerateWireguardKeypair()
	if err != nil {
		t.Fatalf("server keypair: %v", err)
	}
	clientPriv, clientPub, err := wgutil.GenerateWireguardKeypair()
	if err != nil {
		t.Fatalf("client keypair: %v", err)
	}

	inbound := &model.Inbound{
		Listen:   "203.0.113.7",
		Port:     51820,
		Protocol: model.WGKernel,
		Remark:   "wgk",
		Settings: `{"secretKey":"` + serverPriv + `","mtu":1420,"address":["10.0.0.1/24"],"clients":[{"email":"user",` +
			`"privateKey":"` + clientPriv + `","publicKey":"` + clientPub + `","allowedIPs":["10.0.0.2/32"]}]}`,
	}
	client := model.Client{Email: "user", PrivateKey: clientPriv, AllowedIPs: []string{"10.0.0.2/32"}}

	t.Run("raw link", func(t *testing.T) {
		link := (&SubService{}).GetLink(inbound, "user")
		u, err := url.Parse(link)
		if err != nil {
			t.Fatalf("link does not parse: %v\n got: %s", err, link)
		}
		if u.Scheme != "wireguard" {
			t.Fatalf("scheme = %q, want wireguard (got link %q)", u.Scheme, link)
		}
		if got := u.Query().Get("publickey"); got != serverPub {
			t.Fatalf("publickey = %q, want server public key %q", got, serverPub)
		}
	})

	t.Run("json outbound", func(t *testing.T) {
		raw := NewSubJsonService("", "", "", nil).genWireguard(inbound, client)
		if raw == nil {
			t.Fatal("genWireguard = nil, want an outbound for a wgkernel client with a key")
		}
		var outbound struct {
			Protocol string `json:"protocol"`
		}
		if err := json.Unmarshal(raw, &outbound); err != nil {
			t.Fatalf("outbound does not parse: %v", err)
		}
		if outbound.Protocol != "wireguard" {
			t.Fatalf("outbound protocol = %q, want wireguard — xray has no %q outbound and would refuse the config", outbound.Protocol, model.WGKernel)
		}
	})

	t.Run("clash proxy", func(t *testing.T) {
		svc := &SubClashService{SubService: &SubService{}}
		proxy := svc.buildProxy(svc.SubService, inbound, client, map[string]any{}, nil)
		if proxy == nil {
			t.Fatal("buildProxy = nil, want a wireguard proxy for a wgkernel client with a key")
		}
		if proxy["type"] != "wireguard" {
			t.Fatalf("proxy type = %v, want wireguard", proxy["type"])
		}
		if proxy["public-key"] != serverPub {
			t.Fatalf("proxy public-key = %v, want server public key %q", proxy["public-key"], serverPub)
		}
	})
}

// The subId query names its protocols in raw SQL, where no guard can see them,
// and a kind missing from that list is empty in all three formats at once.
func TestGetInboundsBySubIdIncludesWireguard(t *testing.T) {
	tests := []struct {
		name     string
		protocol model.Protocol
		port     int
	}{
		{"xray wireguard", model.WireGuard, 51820},
		{"kernel wireguard", model.WGKernel, 51821},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			initSubDB(t)
			db := database.GetDB()

			in := &model.Inbound{Port: tc.port, Protocol: tc.protocol, Enable: true, Tag: "wg-sub", Settings: `{"secretKey":"x","clients":[]}`}
			if err := db.Create(in).Error; err != nil {
				t.Fatalf("create inbound: %v", err)
			}
			rec := &model.ClientRecord{Email: "u@wg", SubID: "subwg", Enable: true}
			if err := db.Create(rec).Error; err != nil {
				t.Fatalf("create client: %v", err)
			}
			if err := db.Create(&model.ClientInbound{ClientId: rec.Id, InboundId: in.Id}).Error; err != nil {
				t.Fatalf("create link: %v", err)
			}

			s := &SubService{}
			inbounds, err := s.getInboundsBySubId("subwg")
			if err != nil {
				t.Fatalf("getInboundsBySubId: %v", err)
			}
			if len(inbounds) != 1 || inbounds[0].Id != in.Id {
				t.Fatalf("%s inbound not returned for subId, so its subscription is empty: %+v", tc.protocol, inbounds)
			}
		})
	}
}
