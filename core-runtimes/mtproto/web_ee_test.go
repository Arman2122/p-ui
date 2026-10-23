package mtproto

import (
	"net"
	"testing"
	"time"

	"github.com/Arman2122/mtg-proxy/essentials"
	"github.com/Arman2122/mtg-proxy/mtglib"
	"github.com/Arman2122/mtg-proxy/mtglib/obfuscation"
	"github.com/Arman2122/mtg-proxy/nodeweb"
)

func managedFakeTLSClient(t *testing.T, process *running, secret mtglib.Secret, selected ...int) essentials.Conn {
	t.Helper()
	index := 0
	if len(selected) != 0 {
		index = selected[0]
	}
	connection, err := net.DialTimeout("tcp", process.listeners[index].Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	if err := connection.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	transport := fakeTLSClient(t, essentials.WrapNetConn(connection), secret)
	client, err := (obfuscation.Obfuscator{Secret: secret.Key[:]}).SendHandshake(transport, 2, obfuscation.TagPaddedIntermediate)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestDirectWEBEECredentialsShareDerivedDDProfileThroughRotation(t *testing.T) {
	managed, config, secret, account, authority := directWEBFixture(t, 0)
	config.Inbounds[0].Listeners[0].Accept = []string{"ee"}
	applyManagedWEB(t, managed, config, account)
	process := managed.instances[config.Inbounds[0].ID]
	connect := func(secret mtglib.Secret) (essentials.Conn, essentials.Conn) {
		t.Helper()
		dd := mtglib.Secret{Key: secret.Key, Mode: mtglib.SecretModeDD}
		eeCapability, err := nodeweb.Derive(secret, config.Inbounds[0].WEB.Host)
		if err != nil {
			t.Fatal(err)
		}
		ddCapability, err := nodeweb.Derive(dd, config.Inbounds[0].WEB.Host)
		if err != nil || eeCapability.String() != ddCapability.String() {
			t.Fatal("EE source and derived DD WEB capability disagree")
		}
		native := managedFakeTLSClient(t, process, secret)
		web, _ := directWEBSession(t, process.web, config.Inbounds[0].WEB, authority, dd, "", nil)
		roundTripMTProto(t, native)
		roundTripMTProto(t, web)
		for _, client := range config.Inbounds[0].Clients {
			if client.Secret != secret.String() {
				t.Fatal("WEB rewrote the native EE credential")
			}
		}
		return native, web
	}
	native, web := connect(secret)
	rotated, err := mtglib.GenerateSecretMode(mtglib.SecretModeFakeTLS, "cover.example.com")
	if err != nil {
		t.Fatal(err)
	}
	for id, client := range config.Inbounds[0].Clients {
		client.Secret = rotated.String()
		config.Inbounds[0].Clients[id] = client
	}
	applyManagedWEB(t, managed, config, account)
	if managed.instances[config.Inbounds[0].ID] != process {
		t.Fatal("EE rotation restarted native or WEB listeners")
	}
	for _, connection := range []essentials.Conn{native, web} {
		if n, err := connection.Read(make([]byte, 1)); n != 0 || err == nil {
			t.Fatalf("EE rotation retained old authenticated stream: %d %v", n, err)
		}
	}
	_, _ = connect(rotated)
}
