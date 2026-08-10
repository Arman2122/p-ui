package service

import (
	"strconv"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
	wgutil "github.com/Arman2122/p-ui/internal/util/wireguard"
)

// A wgkernel client holds a key and an email but never an id, so any credential
// switch that drops it into the shared `default:` fails with "empty client ID".
func TestWgkernelClientSurvivesTheCredentialSwitches(t *testing.T) {
	setupBulkDB(t)
	inboundSvc := &InboundService{}
	clientSvc := &ClientService{}

	_, seedPublic, err := wgutil.GenerateWireguardKeypair()
	if err != nil {
		t.Fatalf("seed keypair: %v", err)
	}

	var created *model.Inbound
	t.Run("AddInbound accepts a keyed client with no id", func(t *testing.T) {
		in := &model.Inbound{
			Tag:      "wgk-51950",
			Enable:   true,
			Listen:   "0.0.0.0",
			Port:     51950,
			Protocol: model.WGKernel,
			Settings: `{"secretKey":"` + wgTestSecretKey() + `","mtu":1420,"address":["10.0.0.1/24"],` +
				`"clients":[{"email":"seed@wgk","enable":true,"publicKey":"` + seedPublic + `","allowedIPs":["10.0.0.2/32"]}]}`,
		}
		created, _, err = inboundSvc.AddInbound(in)
		if err != nil {
			t.Fatalf("AddInbound: %v", err)
		}
	})
	if created == nil {
		t.Fatal("no inbound to continue with")
	}

	t.Run("AddInboundClient mints the keys instead of demanding an id", func(t *testing.T) {
		add := &model.Inbound{Id: created.Id, Protocol: model.WGKernel, Settings: clientsSettings(t, []model.Client{
			{Email: "alice@wgk", Enable: true},
		})}
		if _, err := clientSvc.AddInboundClient(inboundSvc, add); err != nil {
			t.Fatalf("AddInboundClient: %v", err)
		}
		list, lErr := clientSvc.ListForInbound(nil, created.Id)
		if lErr != nil {
			t.Fatalf("ListForInbound: %v", lErr)
		}
		alice := clientNamed(t, list, "alice@wgk")
		if alice.PrivateKey == "" || alice.PublicKey == "" {
			t.Fatalf("keys not generated for a wgkernel client: %+v", alice)
		}
		if len(alice.AllowedIPs) == 0 {
			t.Fatalf("allowedIPs not allocated for a wgkernel client: %+v", alice)
		}
		if alice.ID != "" {
			t.Fatalf("a wgkernel client must carry no uuid, got %q", alice.ID)
		}
	})

	t.Run("UpdateInboundClient resolves the client by email, not by id", func(t *testing.T) {
		before := clientNamed(t, listForInbound(t, clientSvc, created.Id), "alice@wgk")
		update := &model.Inbound{Id: created.Id, Protocol: model.WGKernel, Settings: clientsSettings(t, []model.Client{
			{Email: "alice@wgk", Enable: true, Comment: "renamed laptop"},
		})}
		if _, err := clientSvc.UpdateInboundClient(inboundSvc, update, "alice@wgk"); err != nil {
			t.Fatalf("UpdateInboundClient: %v", err)
		}
		after := clientNamed(t, listForInbound(t, clientSvc, created.Id), "alice@wgk")
		if after.PublicKey != before.PublicKey || after.PrivateKey != before.PrivateKey {
			t.Fatalf("a metadata edit rotated the keys: was %q/%q now %q/%q",
				before.PrivateKey, before.PublicKey, after.PrivateKey, after.PublicKey)
		}
		if after.Comment != "renamed laptop" {
			t.Fatalf("comment not updated: %q", after.Comment)
		}
	})
}

// wgkernelInbound is the shape the panel's own defaults produce: 10.0.0.1/24 on
// the device, and allocateWireguardAddress handing the first client 10.0.0.2/32.
func wgkernelInbound(port int, address string) *model.Inbound {
	return &model.Inbound{
		Tag:      "wgk-" + strconv.Itoa(port),
		Enable:   true,
		Listen:   "0.0.0.0",
		Port:     port,
		Protocol: model.WGKernel,
		Settings: `{"secretKey":"` + wgTestSecretKey() + `","mtu":1420,"address":["` + address + `"],"clients":[]}`,
	}
}

// TestTwoHostWireguardInboundsCannotShareASubnet: both devices install the same
// connected route into the one host routing table, so `ip route get` resolves a
// client of one inbound onto the other's device and encrypts it to its peer.
func TestTwoHostWireguardInboundsCannotShareASubnet(t *testing.T) {
	setupBulkDB(t)
	svc := &InboundService{}

	if _, _, err := svc.AddInbound(wgkernelInbound(51961, "10.0.0.1/24")); err != nil {
		t.Fatalf("first AddInbound: %v", err)
	}
	_, _, err := svc.AddInbound(wgkernelInbound(51962, "10.0.0.1/24"))
	if err == nil {
		t.Fatal("a second wgkernel inbound was accepted on the same tunnel subnet; every client of one is answered over the other's device")
	}
	if !strings.Contains(err.Error(), "overlaps") {
		t.Fatalf("AddInbound = %v, want an error naming the overlap", err)
	}

	t.Run("a subnet nothing else claims is accepted", func(t *testing.T) {
		if _, _, err := svc.AddInbound(wgkernelInbound(51963, "10.9.0.1/24")); err != nil {
			t.Fatalf("AddInbound on a free subnet: %v", err)
		}
	})

	t.Run("a wider prefix containing one already served is rejected", func(t *testing.T) {
		if _, _, err := svc.AddInbound(wgkernelInbound(51964, "10.0.1.1/16")); err == nil {
			t.Fatal("a /16 covering an existing /24 was accepted; the two collide on every address the /24 hands out")
		}
	})

	t.Run("editing an inbound onto a taken subnet is rejected", func(t *testing.T) {
		free := wgkernelInbound(51965, "10.8.0.1/24")
		created, _, addErr := svc.AddInbound(free)
		if addErr != nil {
			t.Fatalf("AddInbound: %v", addErr)
		}
		moved := wgkernelInbound(51965, "10.0.0.1/24")
		moved.Id = created.Id
		moved.Tag = created.Tag
		if _, _, err := svc.UpdateInbound(moved); err == nil {
			t.Fatal("an inbound was edited onto a subnet another one already serves")
		}
	})
}

// TestTwoClientsCannotShareOnePublicKey: the kernel holds one peer per key, so
// the second client has no tunnel and is billed the first one's traffic.
func TestTwoClientsCannotShareOnePublicKey(t *testing.T) {
	setupBulkDB(t)
	inboundSvc := &InboundService{}
	clientSvc := &ClientService{}

	_, seedPublic, err := wgutil.GenerateWireguardKeypair()
	if err != nil {
		t.Fatalf("seed keypair: %v", err)
	}
	in := wgkernelInbound(51971, "10.10.0.1/24")
	in.Settings = `{"secretKey":"` + wgTestSecretKey() + `","mtu":1420,"address":["10.10.0.1/24"],` +
		`"clients":[{"email":"seed@wgk","enable":true,"publicKey":"` + seedPublic + `","allowedIPs":["10.10.0.2/32"]}]}`
	created, _, err := inboundSvc.AddInbound(in)
	if err != nil {
		t.Fatalf("AddInbound: %v", err)
	}

	t.Run("adding a client on a key another one holds", func(t *testing.T) {
		add := &model.Inbound{Id: created.Id, Protocol: model.WGKernel, Settings: clientsSettings(t, []model.Client{
			{Email: "clone@wgk", Enable: true, PublicKey: seedPublic, AllowedIPs: []string{"10.10.0.3/32"}},
		})}
		_, addErr := clientSvc.AddInboundClient(inboundSvc, add)
		if addErr == nil {
			t.Fatal("a second client was stored on the same public key; the kernel collapses them into one peer and bills whoever wins")
		}
		if !strings.Contains(addErr.Error(), "public key already used") {
			t.Fatalf("AddInboundClient = %v, want an error naming the key collision", addErr)
		}
	})

	t.Run("two new clients in one batch sharing a key", func(t *testing.T) {
		_, batchPublic, kErr := wgutil.GenerateWireguardKeypair()
		if kErr != nil {
			t.Fatalf("keypair: %v", kErr)
		}
		add := &model.Inbound{Id: created.Id, Protocol: model.WGKernel, Settings: clientsSettings(t, []model.Client{
			{Email: "twin-a@wgk", Enable: true, PublicKey: batchPublic, AllowedIPs: []string{"10.10.0.4/32"}},
			{Email: "twin-b@wgk", Enable: true, PublicKey: batchPublic, AllowedIPs: []string{"10.10.0.5/32"}},
		})}
		if _, addErr := clientSvc.AddInboundClient(inboundSvc, add); addErr == nil {
			t.Fatal("two clients in one batch were stored on one public key")
		}
	})

	t.Run("editing a client onto a key another one holds", func(t *testing.T) {
		add := &model.Inbound{Id: created.Id, Protocol: model.WGKernel, Settings: clientsSettings(t, []model.Client{
			{Email: "bob@wgk", Enable: true},
		})}
		if _, addErr := clientSvc.AddInboundClient(inboundSvc, add); addErr != nil {
			t.Fatalf("AddInboundClient: %v", addErr)
		}
		update := &model.Inbound{Id: created.Id, Protocol: model.WGKernel, Settings: clientsSettings(t, []model.Client{
			{Email: "bob@wgk", Enable: true, PublicKey: seedPublic, AllowedIPs: []string{"10.10.0.9/32"}},
		})}
		if _, upErr := clientSvc.UpdateInboundClient(inboundSvc, update, "bob@wgk"); upErr == nil {
			t.Fatal("a client was edited onto the key another client already holds")
		}
	})
}

func listForInbound(t *testing.T, svc *ClientService, inboundId int) []model.Client {
	t.Helper()
	list, err := svc.ListForInbound(nil, inboundId)
	if err != nil {
		t.Fatalf("ListForInbound: %v", err)
	}
	return list
}

func clientNamed(t *testing.T, list []model.Client, email string) model.Client {
	t.Helper()
	for _, c := range list {
		if c.Email == email {
			return c
		}
	}
	t.Fatalf("client %q not attached, have %v", email, emailsOf(list))
	return model.Client{}
}
