package ssh

import (
	"context"
	"errors"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	gossh "golang.org/x/crypto/ssh"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func TestSSHHotProofUpdatesPreserveOtherClientTCPUDPAndAccounting(t *testing.T) {
	a, b := userN(0), userN(1)
	a.Email, b.Email = uuid.NewString(), uuid.NewString()
	accountA, err := limits.New(limits.Policy{Term: "a", Revision: 1, Enabled: true, CreditCeiling: 1 << 20, Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}})
	if err != nil {
		t.Fatal(err)
	}
	accountB, err := limits.New(limits.Policy{Term: "b", Revision: 1, Enabled: true, CreditCeiling: 1 << 20, Upload: limits.Rate{Mode: limits.Limited, BitsPerSecond: 1_000_000, BurstBytes: 65536}, Download: limits.Rate{Mode: limits.Limited, BitsPerSecond: 1_000_000, BurstBytes: 65536}})
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var pause atomic.Bool
	var destinations atomic.Int32
	engine := New(Deps{
		LoadHostKey: func() (string, error) { return testHostKeyPEM(t), nil },
		Governor: func(client string) (*limits.Account, bool) {
			if client == a.Email {
				if pause.CompareAndSwap(true, false) {
					close(entered)
					<-release
				}
				return accountA, true
			}
			return accountB, client == b.Email
		},
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			destinations.Add(1)
			return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, address)
		},
	})
	t.Cleanup(func() { _ = engine.StopAll(context.Background()) })
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	inbound := core.Instance{ID: 42, Kind: Kind, Tag: "hot-proof", Listen: "127.0.0.1", Port: freePort(t), Enable: true, Settings: `{"udpgw":true}`, Users: []core.User{a, b}}
	descriptor := func(client string) admission.Descriptor {
		return admission.Descriptor{ClientID: client, CredentialGeneration: 1, PlacementID: uuid.NewString(), PlacementRevision: 1, InboundInstanceID: uuid.NewString(), InboundTag: inbound.Tag}
	}
	da, db := descriptor(a.Email), descriptor(b.Email)
	broker := admission.New("ssh", func() (time.Time, error) { return time.Now(), nil })
	apply := func(revision uint64, descriptors []admission.Descriptor, changed ...admission.Subject) {
		t.Helper()
		binding, err := broker.Prepare(descriptors)
		if err != nil {
			t.Fatal(err)
		}
		if err := engine.InvalidateAuthentication(t.Context(), changed); err != nil {
			t.Fatal(err)
		}
		if err := engine.Reconcile(admission.WithBinding(t.Context(), binding), []core.Instance{inbound}); err != nil {
			t.Fatal(err)
		}
		if err := broker.Commit(revision, binding); err != nil {
			t.Fatal(err)
		}
	}
	apply(1, []admission.Descriptor{da, db})
	connect := func(user core.User) (*gossh.Client, error) {
		return gossh.Dial("tcp", net.JoinHostPort(inbound.Listen, strconv.Itoa(inbound.Port)), clientConfig(core.CredString(user.Credentials, core.CredUsername), core.CredString(user.Credentials, core.CredPassword)))
	}
	clientB, err := connect(b)
	if err != nil {
		t.Fatal(err)
	}
	defer clientB.Close()
	echo := echoListener(t)
	tcpB, err := clientB.Dial("tcp", echo.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer tcpB.Close()
	udpB := openUDPGW(t, clientB, udpgwDefaultPort)
	defer udpB.Close()
	udp := udpEcho(t)
	verifyB := func() {
		t.Helper()
		before := accountB.Snapshot()
		if _, err := tcpB.Write([]byte("same-tcp")); err != nil {
			t.Fatalf("unrelated TCP carrier was interrupted: %v", err)
		}
		if got, err := readWithin(tcpB, time.Second); err != nil || string(got) != "same-tcp" {
			t.Fatalf("unrelated TCP carrier lost continuity: %q %v", got, err)
		}
		if _, err := udpB.Write(udpgwFrame(0, 7, udp, []byte("same-udp"))); err != nil {
			t.Fatal(err)
		}
		_, id, from, got := readUDPGWFrame(t, udpB)
		if id != 7 || from != udp || string(got) != "same-udp" {
			t.Fatalf("unrelated UDP carrier lost continuity: %d %s %q", id, from, got)
		}
		after := accountB.Snapshot()
		if after.AdmittedUpload <= before.AdmittedUpload || after.AdmittedDownload <= before.AdmittedDownload || after.Policy != before.Policy {
			t.Fatal("hot credential edit reset or replaced the unrelated accounting authority")
		}
		engine.mu.Lock()
		server := engine.instances[inbound.ID].srv
		server.mu.RLock()
		for connection := range server.conns {
			if connection.email == b.Email && connection.account != accountB {
				t.Error("unchanged transport acquired a replacement accounting/rate object")
			}
		}
		server.mu.RUnlock()
		engine.mu.Unlock()
	}
	verifyB()
	clientA, err := connect(a)
	if err != nil {
		t.Fatal(err)
	}
	defer clientA.Close()
	forwardA, err := clientA.Dial("tcp", echo.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer forwardA.Close()
	pause.Store(true)
	oldDone := make(chan *gossh.Client, 1)
	go func() { connection, _ := connect(a); oldDone <- connection }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("funded old authentication did not reach the overlap boundary")
	}
	rotated := a
	rotated.Credentials = map[string]any{core.CredUsername: "user0", core.CredPassword: "replacement-password"}
	inbound.Users = []core.User{rotated, b}
	da.CredentialGeneration++
	changedA := admission.Subject{ClientID: a.Email, InboundTag: inbound.Tag}
	apply(2, []admission.Descriptor{da, db}, changedA)
	close(release)
	old := <-oldDone
	if old != nil {
		defer old.Close()
		if forwarded, err := old.Dial("tcp", echo.Addr().String()); err == nil {
			_ = forwarded.Close()
			t.Fatal("old funded password adopted the new credential proof")
		}
	}
	if _, err := readWithin(forwardA, time.Second); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("rekey kept the changed client's prior carrier alive")
	}
	verifyB()
	newA, err := connect(rotated)
	if err != nil {
		t.Fatal(err)
	}
	roundTrip(t, newA, echo.Addr(), []byte("new-proof"))
	_ = newA.Close()
	inbound.Users = []core.User{b}
	apply(3, []admission.Descriptor{db}, changedA)
	verifyB()
	if revoked, err := connect(rotated); err == nil {
		_ = revoked.Close()
		t.Fatal("removed credential authenticated")
	}
	inbound.Users = []core.User{rotated, b}
	da.CredentialGeneration++
	apply(4, []admission.Descriptor{da, db}, changedA)
	verifyB()
	inbound.Users = nil
	apply(5, nil, changedA, admission.Subject{ClientID: b.Email, InboundTag: inbound.Tag})
	if anonymous, err := connect(rotated); err == nil {
		_ = anonymous.Close()
		t.Fatal("empty credential set allowed anonymous authentication")
	}
	if _, err := readWithin(tcpB, time.Second); err == nil || errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("final empty credential set retained an authenticated carrier")
	}
}
