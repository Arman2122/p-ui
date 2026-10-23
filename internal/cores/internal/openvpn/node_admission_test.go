package openvpn

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/google/uuid"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func nodeAdmissionDescriptor(client string) admission.Descriptor {
	return admission.Descriptor{ClientID: client, CredentialGeneration: 1, PlacementID: uuid.NewString(), PlacementRevision: 1, InboundInstanceID: uuid.NewString(), InboundTag: "openvpn-fixture"}
}

func nodeAdmissionGrant(client string) governor.Grant {
	return governor.Grant{ID: uuid.NewString(), ClientID: client, Policy: limits.Policy{Term: uuid.NewString(), Revision: 1, Enabled: true, CreditCeiling: 1024, Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}}}
}

func TestNodeManagementAdmissionReceiptCannotAuthenticateOriginalAttempt(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	a := NodeClient{ClientID: uuid.NewString(), Username: "alice", Password: "alice-password", Addresses: []netip.Addr{netip.MustParseAddr("10.8.0.2")}}
	b := NodeClient{ClientID: uuid.NewString(), Username: "bob", Password: "bob-password", Addresses: []netip.Addr{netip.MustParseAddr("10.8.0.3")}}
	da, db := nodeAdmissionDescriptor(a.ClientID), nodeAdmissionDescriptor(b.ClientID)
	broker := admission.New("native", func() (time.Time, error) { return time.Now(), nil })
	binding, err := broker.Prepare([]admission.Descriptor{da, db})
	if err != nil {
		t.Fatal(err)
	}
	auth := NewNodeAuthenticator(NodeConfiguration{Subnet: netip.MustParsePrefix("10.8.0.0/24"), Clients: []NodeClient{a, b}})
	if err := auth.BindAdmission(binding, da.InboundTag); err != nil {
		t.Fatal(err)
	}
	if err := broker.Commit(9, binding); err != nil {
		t.Fatal(err)
	}
	grantA, grantB := nodeAdmissionGrant(a.ClientID), nodeAdmissionGrant(b.ClientID)
	auth.Grants([]governor.Grant{grantB})
	server, daemon := net.Pipe()
	defer daemon.Close()
	done := make(chan error, 1)
	go func() { done <- auth.Run(ctx, server, nil) }()
	t.Cleanup(func() { cancel(); _ = daemon.Close(); <-done })
	reader := bufio.NewReader(daemon)
	read := func(want string) {
		t.Helper()
		_ = daemon.SetReadDeadline(time.Now().Add(time.Second))
		line, err := reader.ReadString('\n')
		if err != nil || line != want {
			t.Fatalf("management admission response=%q err=%v want=%q", line, err, want)
		}
	}
	send := func(client NodeClient, kind string, cid int) {
		t.Helper()
		_ = daemon.SetWriteDeadline(time.Now().Add(time.Second))
		if _, err := fmt.Fprintf(daemon, ">CLIENT:%s,%d,0\n>CLIENT:ENV,username=%s\n>CLIENT:ENV,password=%s\n>CLIENT:ENV,END\n", kind, cid, client.Username, client.Password); err != nil {
			t.Fatal(err)
		}
	}
	read("state on\n")
	read("hold release\n")
	fmt.Fprint(daemon, "SUCCESS: state\nSUCCESS: hold\n")
	send(b, "CONNECT", 2)
	read("client-auth 2 0\n")
	read("ifconfig-push 10.8.0.3 255.255.255.0\n")
	read("END\n")
	fmt.Fprint(daemon, "SUCCESS: authentication\n")
	send(a, "CONNECT", 1)
	request, err := broker.Next(ctx)
	if err != nil || request.ClientId != a.ClientID || request.PlacementId != da.PlacementID || request.InboundInstanceId != da.InboundInstanceID || request.DeploymentRevision != 9 {
		t.Fatalf("native demand lost authenticated scope=%+v err=%v", request, err)
	}
	installed := make(chan struct{})
	go func() { auth.Grants([]governor.Grant{grantA, grantB}); close(installed) }()
	select {
	case <-installed:
	case <-time.After(time.Second):
		t.Fatal("master wait held OpenVPN authentication lock")
	}
	if err := broker.Complete(&nodev1.ActivationResult{RequestId: request.RequestId, ClientId: a.ClientID, TermId: grantA.Policy.Term}); err != nil {
		t.Fatal(err)
	}
	read("client-deny 1 0 authorization-unavailable\n")
	fmt.Fprint(daemon, "SUCCESS: denied\n")
	if _, exists := auth.sessions[1]; exists {
		t.Fatal("admission receipt reserved an OpenVPN session")
	}
	send(a, "CONNECT", 3)
	read("client-auth 3 0\n")
	read("ifconfig-push 10.8.0.2 255.255.255.0\n")
	read("END\n")
	fmt.Fprint(daemon, "SUCCESS: authentication\n")
	da.CredentialGeneration++
	next, err := broker.Prepare([]admission.Descriptor{da, db})
	if err != nil || auth.BindAdmission(next, da.InboundTag) != nil || broker.Commit(10, next) != nil {
		t.Fatalf("credential proof rotation=%v", err)
	}
	send(a, "REAUTH", 3)
	read("client-deny 3 0 authorization-unavailable\n")
	fmt.Fprint(daemon, "SUCCESS: denied\n")
	send(b, "REAUTH", 2)
	read("client-auth 2 0\n")
	read("ifconfig-push 10.8.0.3 255.255.255.0\n")
	read("END\n")
	fmt.Fprint(daemon, "SUCCESS: authentication\n")
}

type pausedNodeAuthorizationClock struct {
	entered chan struct{}
	release chan struct{}
}

func (c pausedNodeAuthorizationClock) Now() (time.Time, error) {
	close(c.entered)
	<-c.release
	return time.Now(), nil
}

func TestNodeManagementPausedCredentialCannotUseReassignedAlias(t *testing.T) {
	client := NodeClient{ClientID: uuid.NewString(), Username: "alice", Password: "old-password", Addresses: []netip.Addr{netip.MustParseAddr("10.8.0.2")}}
	descriptor := nodeAdmissionDescriptor(client.ClientID)
	broker := admission.New("native", func() (time.Time, error) { return time.Now(), nil })
	binding, _ := broker.Prepare([]admission.Descriptor{descriptor})
	auth := NewNodeAuthenticator(NodeConfiguration{Clients: []NodeClient{client}})
	if err := auth.BindAdmission(binding, descriptor.InboundTag); err != nil {
		t.Fatal(err)
	}
	if err := broker.Commit(1, binding); err != nil {
		t.Fatal(err)
	}
	auth.Grants([]governor.Grant{nodeAdmissionGrant(client.ClientID)})
	clock := pausedNodeAuthorizationClock{entered: make(chan struct{}), release: make(chan struct{})}
	auth.SetClock(clock)
	event := nodeEvent{kind: "CONNECT", cid: 1, env: map[string]string{"username": client.Username, "password": client.Password}}
	result := make(chan error, 1)
	go func() { _, err := auth.authorize(t.Context(), event, time.Now()); result <- err }()
	<-clock.entered
	nextClient := client
	nextClient.ClientID, nextClient.Password = uuid.NewString(), "new-password"
	nextDescriptor := nodeAdmissionDescriptor(nextClient.ClientID)
	next, _ := broker.Prepare([]admission.Descriptor{nextDescriptor})
	if err := broker.Commit(2, next); err != nil {
		t.Fatal(err)
	}
	close(clock.release)
	if err := <-result; !errors.Is(err, errNodeAuthorization) || len(auth.sessions) != 0 {
		t.Fatalf("paused credential acquired reused alias authority=%v", err)
	}
	fresh := NewNodeAuthenticator(NodeConfiguration{Clients: []NodeClient{nextClient}})
	if err := fresh.BindAdmission(next, descriptor.InboundTag); err != nil {
		t.Fatal(err)
	}
	fresh.Grants([]governor.Grant{nodeAdmissionGrant(nextClient.ClientID)})
	event.env["password"] = nextClient.Password
	if _, err := fresh.authorize(t.Context(), event, time.Now()); err != nil {
		t.Fatalf("new alias credential could not authenticate=%v", err)
	}
	event.env["password"] = client.Password
	if _, err := fresh.authorize(t.Context(), event, time.Now()); !errors.Is(err, errNodeCredentials) {
		t.Fatalf("old alias password was accepted=%v", err)
	}
}
