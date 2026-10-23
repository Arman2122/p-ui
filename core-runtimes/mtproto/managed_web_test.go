package mtproto

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Arman2122/mtg-proxy/essentials"
	"github.com/Arman2122/mtg-proxy/mtglib"
	"github.com/Arman2122/mtg-proxy/mtglib/obfuscation"
	network "github.com/Arman2122/mtg-proxy/network/v2"
	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/node/state"
)

func managedWEBFixture(t *testing.T, ipLimit int) (*Managed, Configuration, mtglib.Secret, *limits.Account) {
	t.Helper()
	return managedWEBFixtureWithAdmission(t, ipLimit, nil, nil)
}

func managedWEBFixtureWithAdmission(t *testing.T, ipLimit int, demand admission.Handler, lookup Lookup) (*Managed, Configuration, mtglib.Secret, *limits.Account) {
	t.Helper()
	secret, err := mtglib.GenerateSecretMode(mtglib.SecretModeFakeTLS, "cover.example.com")
	if err != nil {
		t.Fatal(err)
	}
	account, err := limits.New(limits.Policy{
		Term: "managed-web", Revision: 1, Enabled: true, QuotaUnlimited: true,
		Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited},
	})
	if err != nil {
		t.Fatal(err)
	}
	addresses := make([]string, 2)
	listeners := make([]net.Listener, 2)
	for i := range listeners {
		listeners[i], err = net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addresses[i] = listeners[i].Addr().String()
	}
	for _, listener := range listeners {
		_ = listener.Close()
	}
	config := Configuration{Inbounds: []Inbound{{
		ID: uuid.NewString(), Listeners: []Listener{{Bind: addresses[0], Accept: []string{"dd"}}},
		Clients: map[string]Client{uuid.NewString(): {Secret: secret.String(), IPLimit: ipLimit}}, WEB: &WEB{Bind: addresses[1], Host: "relay.example.com", CoverURL: "https://cover.example.com"},
	}}}
	managed := &Managed{stateDir: t.TempDir(), network: telegramNetwork{Network: network.New(nil, "penhoon-test", time.Second, time.Second, time.Second, net.KeepAliveConfig{}, 0), target: telegramEcho(t)}}
	managed.store, err = state.Open(filepath.Join(managed.stateDir, "runtime.db"), state.Identity{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = managed.store.Close() })
	t.Cleanup(func() { _ = managed.Close() })
	if lookup == nil {
		lookup = func(string) (*limits.Account, bool) { return account, true }
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := managed.Validate(encoded); err != nil {
		t.Fatal(err)
	}
	if err := managed.Apply(admission.WithHandler(t.Context(), demand), encoded, lookup); err != nil {
		t.Fatal(err)
	}
	return managed, config, secret, account
}

func applyManagedWEB(t *testing.T, managed *Managed, config Configuration, account *limits.Account) {
	t.Helper()
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := managed.Validate(encoded); err != nil {
		t.Fatal(err)
	}
	if err := managed.Apply(t.Context(), encoded, func(string) (*limits.Account, bool) { return account, true }); err != nil {
		t.Fatal(err)
	}
}

func managedNativeClient(t *testing.T, process *running, secret mtglib.Secret) essentials.Conn {
	t.Helper()
	connection, err := net.DialTimeout("tcp", process.listeners[0].Addr().String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	if err := connection.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	client, err := (obfuscation.Obfuscator{Secret: secret.Key[:]}).SendHandshake(essentials.WrapNetConn(connection), 2, obfuscation.TagPaddedIntermediate)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func roundTripMTProto(t *testing.T, connection essentials.Conn) {
	t.Helper()
	payload := bytes.Repeat([]byte{23}, 512)
	if _, err := connection.Write(bytes.Clone(payload)); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(connection, got); err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("managed MTProto echo: %v", err)
	}
}

func TestManagedWEBAndNativeShareAddressAdmission(t *testing.T) {
	managed, config, secret, _ := managedWEBFixture(t, 1)
	process := managed.instances[config.Inbounds[0].ID]
	native := managedNativeClient(t, process, secret)
	roundTripMTProto(t, native)
	web, _ := webSession(t, process.web, secret)
	_, _ = web.Write(bytes.Repeat([]byte{24}, 8192))
	if n, err := web.Read(make([]byte, 1)); n != 0 || err == nil {
		t.Fatalf("WEB bypassed shared client IP limit: %d %v", n, err)
	}
	roundTripMTProto(t, native)
}

func TestManagedWEBHotRotationAndDeadListenerRepair(t *testing.T) {
	managed, config, secret, account := managedWEBFixture(t, 0)
	process := managed.instances[config.Inbounds[0].ID]
	native := managedNativeClient(t, process, secret)
	web, _ := webSession(t, process.web, secret)
	roundTripMTProto(t, native)
	roundTripMTProto(t, web)
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
		t.Fatal("secret rotation restarted native or WEB listeners")
	}
	for _, connection := range []essentials.Conn{native, web} {
		if n, err := connection.Read(make([]byte, 1)); n != 0 || err == nil {
			t.Fatalf("rotated secret kept an old stream alive: %d %v", n, err)
		}
	}
	current, _ := webSession(t, process.web, rotated)
	roundTripMTProto(t, current)
	_ = process.web.listener.Close()
	deadline := time.Now().Add(3 * time.Second)
	for !process.web.exited.Load() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if managed.Check() == nil {
		t.Fatal("closed WEB listener was not detected")
	}
	applyManagedWEB(t, managed, config, account)
	repaired := managed.instances[config.Inbounds[0].ID]
	if repaired == process || managed.Check() != nil {
		t.Fatal("same deployment acknowledged a dead WEB listener")
	}
	reconnected, _ := webSession(t, repaired.web, rotated)
	roundTripMTProto(t, reconnected)
}

func TestManagedWEBKeyLossRequiresOriginalAuthority(t *testing.T) {
	managed, config, secret, account := managedWEBFixture(t, 0)
	files, err := filepath.Glob(filepath.Join(managed.stateDir, "web-*.key"))
	if err != nil || len(files) != 1 {
		t.Fatalf("key inventory: %v %v", files, err)
	}
	original, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := managed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(files[0]); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(string) (*limits.Account, bool) { return account, true }
	if err := managed.Apply(t.Context(), encoded, lookup); !errors.Is(err, state.ErrIdentity) {
		t.Fatalf("lost WEB key was regenerated: %v", err)
	}
	if err := os.WriteFile(files[0], bytes.Repeat([]byte{9}, 32), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := managed.Apply(t.Context(), encoded, lookup); !errors.Is(err, state.ErrIdentity) {
		t.Fatalf("replaced WEB key was accepted: %v", err)
	}
	if err := os.WriteFile(files[0], original, 0o600); err != nil {
		t.Fatal(err)
	}
	applyManagedWEB(t, managed, config, account)
	client, _ := webSession(t, managed.instances[config.Inbounds[0].ID].web, secret)
	roundTripMTProto(t, client)
}
