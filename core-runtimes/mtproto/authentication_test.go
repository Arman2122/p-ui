package mtproto

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/Arman2122/mtg-proxy/essentials"
	"github.com/Arman2122/mtg-proxy/mtglib"
	"github.com/Arman2122/mtg-proxy/mtglib/obfuscation"
	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func TestMTProtoHotProofEditsPreserveOtherClientNativeWEBAndAccounting(t *testing.T) {
	managed, config, secretB, accountB, authority := directWEBFixture(t, 0)
	policy := accountB.Snapshot().Policy
	policy.Revision++
	policy.QuotaUnlimited, policy.CreditCeiling = false, 1<<20
	policy.Upload = limits.Rate{Mode: limits.Limited, BitsPerSecond: 1_000_000, BurstBytes: 65536}
	policy.Download = policy.Upload
	if err := accountB.Apply(policy); err != nil {
		t.Fatal(err)
	}
	var idB string
	for id := range config.Inbounds[0].Clients {
		idB = id
	}
	idA := uuid.NewString()
	secretA, err := mtglib.GenerateSecretMode(mtglib.SecretModeFakeTLS, "cover.example.com")
	if err != nil {
		t.Fatal(err)
	}
	accountA, err := limits.New(limits.Policy{Term: "a-hot", Revision: 1, Enabled: true, CreditCeiling: 1 << 20, Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}})
	if err != nil {
		t.Fatal(err)
	}
	config.Inbounds[0].Clients[idA] = Client{Secret: secretA.String()}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	config.Inbounds[0].Listeners = append(config.Inbounds[0].Listeners, Listener{Bind: address, Accept: []string{"ee"}})
	broker := admission.New("mtproto", func() (time.Time, error) { return time.Now(), nil })
	descriptor := func(client string) admission.Descriptor {
		return admission.Descriptor{ClientID: client, CredentialGeneration: 1, PlacementID: uuid.NewString(), PlacementRevision: 1, InboundInstanceID: uuid.NewString(), InboundTag: config.Inbounds[0].ID}
	}
	da, db := descriptor(idA), descriptor(idB)
	apply := func(revision uint64, descriptors []admission.Descriptor, changed ...admission.Subject) {
		t.Helper()
		encoded, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		if err := managed.Validate(encoded); err != nil {
			t.Fatal(err)
		}
		binding, err := broker.Prepare(descriptors)
		if err != nil {
			t.Fatal(err)
		}
		if err := managed.InvalidateAuthentication(t.Context(), changed); err != nil {
			t.Fatal(err)
		}
		if err := managed.Apply(admission.WithBinding(t.Context(), binding), encoded, func(client string) (*limits.Account, bool) {
			if client == idA {
				return accountA, true
			}
			return accountB, client == idB
		}); err != nil {
			t.Fatal(err)
		}
		if err := broker.Commit(revision, binding); err != nil {
			t.Fatal(err)
		}
	}
	apply(1, []admission.Descriptor{da, db})
	process := managed.instances[config.Inbounds[0].ID]
	nativeB := managedNativeClient(t, process, secretB)
	eeB := managedFakeTLSClient(t, process, secretB, 1)
	webB, carrierB := directWEBSession(t, process.web, config.Inbounds[0].WEB, authority, secretB, "", nil)
	defer carrierB.Close()
	verifyB := func() {
		t.Helper()
		before := accountB.Snapshot()
		for _, connection := range []essentials.Conn{nativeB, eeB, webB} {
			roundTripMTProto(t, connection)
		}
		after := accountB.Snapshot()
		if after.AdmittedUpload <= before.AdmittedUpload || after.AdmittedDownload <= before.AdmittedDownload || after.Policy != before.Policy {
			t.Fatal("hot credential edit reset unrelated MTProto accounting authority")
		}
		if managed.instances[config.Inbounds[0].ID] != process {
			t.Fatal("credential edit restarted the shared native/WEB runtime")
		}
	}
	verifyB()
	ambiguous := secretB
	ambiguous.Host = "another-cover.example.com"
	config.Inbounds[0].Clients[idA] = Client{Secret: ambiguous.String()}
	invalid, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := managed.Validate(invalid); err == nil || err.Error() != "ambiguous MTProto client credential" {
		t.Fatalf("same key with another hostname can select another accounting identity: %v", err)
	}
	config.Inbounds[0].Clients[idA] = Client{Secret: secretA.String()}
	verifyB()
	nativeA := managedNativeClient(t, process, secretA)
	webA, carrierA := directWEBSession(t, process.web, config.Inbounds[0].WEB, authority, secretA, "", nil)
	defer carrierA.Close()
	roundTripMTProto(t, nativeA)
	roundTripMTProto(t, webA)
	changedA := admission.Subject{ClientID: idA, InboundTag: config.Inbounds[0].ID}
	rotated, err := mtglib.GenerateSecretMode(mtglib.SecretModeFakeTLS, "cover.example.com")
	if err != nil {
		t.Fatal(err)
	}
	config.Inbounds[0].Clients[idA] = Client{Secret: rotated.String()}
	da.CredentialGeneration++
	apply(2, []admission.Descriptor{da, db}, changedA)
	for _, connection := range []essentials.Conn{nativeA, webA} {
		if n, err := connection.Read(make([]byte, 1)); n != 0 || !closedAuthentication(err) {
			t.Fatalf("changed credential retained its old carrier: %d %v", n, err)
		}
	}
	verifyB()
	fresh := managedNativeClient(t, process, rotated)
	roundTripMTProto(t, fresh)
	// A placement-only change must revoke old carriers even with an identical key.
	da.PlacementRevision++
	apply(3, []admission.Descriptor{da, db}, changedA)
	if n, err := fresh.Read(make([]byte, 1)); n != 0 || !closedAuthentication(err) {
		t.Fatalf("retired placement retained a funded carrier: %d %v", n, err)
	}
	verifyB()
	delete(config.Inbounds[0].Clients, idA)
	apply(4, []admission.Descriptor{db}, changedA)
	verifyB()
	config.Inbounds[0].Clients[idA] = Client{Secret: rotated.String()}
	da.CredentialGeneration++
	apply(5, []admission.Descriptor{da, db}, changedA)
	verifyB()
	roundTripMTProto(t, managedNativeClient(t, process, rotated))
	config.Inbounds = nil
	apply(6, nil, changedA, admission.Subject{ClientID: idB, InboundTag: db.InboundTag})
	if len(managed.instances) != 0 {
		t.Fatal("empty desired graph retained an anonymous listener")
	}
}

type pausedRead struct {
	essentials.Conn
	entered chan struct{}
	release chan struct{}
	first   bool
}

func (p *pausedRead) Read(data []byte) (int, error) {
	if !p.first {
		p.first = true
		close(p.entered)
		<-p.release
	}
	return p.Conn.Read(data)
}

func TestMTProtoOldSnapshotCannotRegisterAfterHotProofOrKeyRotation(t *testing.T) {
	for _, change := range []string{"proof", "key"} {
		t.Run(change, func(t *testing.T) {
			managed, config, secret, account, _ := directWEBFixture(t, 0)
			var clientID string
			for id := range config.Inbounds[0].Clients {
				clientID = id
			}
			descriptor := admission.Descriptor{ClientID: clientID, CredentialGeneration: 1, PlacementID: uuid.NewString(), PlacementRevision: 1, InboundInstanceID: uuid.NewString(), InboundTag: config.Inbounds[0].ID}
			broker := admission.New("mtproto", func() (time.Time, error) { return time.Now(), nil })
			apply := func(revision uint64) {
				t.Helper()
				encoded, err := json.Marshal(config)
				if err != nil {
					t.Fatal(err)
				}
				ctx := t.Context()
				var binding *admission.Binding
				if change == "proof" {
					binding, err = broker.Prepare([]admission.Descriptor{descriptor})
					if err != nil {
						t.Fatal(err)
					}
					ctx = admission.WithBinding(ctx, binding)
				}
				if err := managed.Apply(ctx, encoded, func(string) (*limits.Account, bool) { return account, true }); err != nil {
					t.Fatal(err)
				}
				if binding != nil {
					if err := broker.Commit(revision, binding); err != nil {
						t.Fatal(err)
					}
				}
			}
			apply(1)
			process := managed.instances[config.Inbounds[0].ID]
			server, client := net.Pipe()
			defer client.Close()
			_ = client.SetDeadline(time.Now().Add(3 * time.Second))
			barrier := &pausedRead{Conn: essentials.WrapNetConn(server), entered: make(chan struct{}), release: make(chan struct{})}
			done := make(chan struct{})
			go func() { process.proxy.ServeConn(barrier); close(done) }()
			defer func() {
				select {
				case <-barrier.release:
				default:
					close(barrier.release)
				}
				_ = client.Close()
				<-done
			}()
			select {
			case <-barrier.entered:
			case <-time.After(time.Second):
				t.Fatal("handshake did not capture its original secret snapshot")
			}
			if change == "key" {
				rotated, err := mtglib.GenerateSecretMode(mtglib.SecretModeFakeTLS, "cover.example.com")
				if err != nil {
					t.Fatal(err)
				}
				config.Inbounds[0].Clients[clientID] = Client{Secret: rotated.String()}
			} else {
				descriptor.CredentialGeneration++
			}
			apply(2)
			close(barrier.release)
			transport := fakeTLSClient(t, essentials.WrapNetConn(client), secret)
			obfuscated, err := (obfuscation.Obfuscator{Secret: secret.Key[:]}).SendHandshake(transport, 2, obfuscation.TagPaddedIntermediate)
			if err == nil {
				_, _ = obfuscated.Write(bytes.Repeat([]byte{31}, 512))
				if n, readErr := obfuscated.Read(make([]byte, 1)); n != 0 || !closedAuthentication(readErr) {
					t.Fatalf("stale authenticated snapshot was registered: %d %v", n, readErr)
				}
			}
			if snapshot := account.Snapshot(); snapshot.AdmittedUpload != 0 || snapshot.AdmittedDownload != 0 {
				t.Fatal("stale snapshot consumed the replacement client's authority")
			}
		})
	}
}

func closedAuthentication(err error) bool {
	var networkError net.Error
	return err != nil && (!errors.As(err, &networkError) || !networkError.Timeout())
}
