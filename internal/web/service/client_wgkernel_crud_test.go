package service

import (
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
