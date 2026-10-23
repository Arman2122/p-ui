package mtproto

import (
	"context"
	"encoding/json"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Arman2122/mtg-proxy/essentials"
	"github.com/Arman2122/mtg-proxy/mtglib"

	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func TestAuthenticatedMTProtoAndWEBDemandCannotForward(t *testing.T) {
	var current atomic.Pointer[limits.Account]
	events := make(chan [2]string, 16)
	demand := func(_ context.Context, client, inbound string) error {
		events <- [2]string{client, inbound}
		return nil
	}
	lookup := func(string) (*limits.Account, bool) { account := current.Load(); return account, account != nil }
	managed, config, secret, _ := managedWEBFixtureWithAdmission(t, 0, demand, lookup)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	config.Inbounds[0].Listeners = append(config.Inbounds[0].Listeners, Listener{Bind: address, Accept: []string{"ee"}})
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := managed.Apply(admission.WithHandler(t.Context(), demand), encoded, lookup); err != nil {
		t.Fatal(err)
	}
	process := managed.instances[config.Inbounds[0].ID]
	var clientID string
	for id := range config.Inbounds[0].Clients {
		clientID = id
	}
	deny := func(connection essentials.Conn) {
		t.Helper()
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(time.Second))
		_, _ = connection.Write([]byte("receipt is not forwarding authority"))
		if n, err := connection.Read(make([]byte, 1)); n != 0 || err == nil {
			t.Fatalf("demand receipt forwarded MTProto bytes: %d %v", n, err)
		}
	}
	for _, kind := range []string{"dd", "ee", "web"} {
		var connection essentials.Conn
		switch kind {
		case "dd":
			connection = managedNativeClient(t, process, secret)
		case "ee":
			connection = managedFakeTLSClient(t, process, secret, 1)
		case "web":
			connection, _ = webSession(t, process.web, secret)
		}
		deny(connection)
		select {
		case event := <-events:
			if event != [2]string{clientID, config.Inbounds[0].ID} {
				t.Fatalf("wrong MTProto demand identity for %s: %v", kind, event)
			}
		case <-time.After(time.Second):
			t.Fatalf("authenticated %s attempt produced no demand", kind)
		}
	}
	wrong, err := mtglib.GenerateSecretMode(mtglib.SecretModeDD, "")
	if err != nil {
		t.Fatal(err)
	}
	deny(managedNativeClient(t, process, wrong))
	if len(events) != 0 {
		t.Fatal("invalid MTProto secret produced demand")
	}
	account, err := limits.New(limits.Policy{Term: "funded-after-demand", Revision: 1, Enabled: true, CreditCeiling: 4096, Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}})
	if err != nil {
		t.Fatal(err)
	}
	current.Store(account)
	valid := managedFakeTLSClient(t, process, secret, 1)
	roundTripMTProto(t, valid)
	web, _ := webSession(t, process.web, secret)
	roundTripMTProto(t, web)
	if len(events) != 0 {
		t.Fatal("funded MTProto or WEB traffic emitted demand")
	}
	account.Quarantine()
	deny(managedNativeClient(t, process, secret))
	select {
	case event := <-events:
		if event != [2]string{clientID, config.Inbounds[0].ID} {
			t.Fatalf("wrong quarantined account demand: %v", event)
		}
	case <-time.After(time.Second):
		t.Fatal("denied existing MTProto account produced no demand")
	}
}

func TestMTProtoDemandRejectsMixedListenerEntryModes(t *testing.T) {
	managed, config, _, _ := managedWEBFixture(t, 0)
	config.Inbounds[0].Listeners[0].Accept = []string{"ee", "dd"}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := managed.Validate(encoded); err == nil || err.Error() != "invalid MTProto listener modes" {
		t.Fatalf("unservable mixed entry modes were accepted: %v", err)
	}
}
