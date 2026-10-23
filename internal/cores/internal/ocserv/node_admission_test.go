package ocserv

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/radius"
)

func nodeAdmissionDescriptor(client string) admission.Descriptor {
	return admission.Descriptor{ClientID: client, CredentialGeneration: 1, PlacementID: uuid.NewString(), PlacementRevision: 1, InboundInstanceID: uuid.NewString(), InboundTag: "ocserv-fixture"}
}

func nodeAdmissionGrant(client string) governor.Grant {
	return governor.Grant{ID: uuid.NewString(), ClientID: client, Policy: limits.Policy{Term: uuid.NewString(), Revision: 1, Enabled: true, CreditCeiling: 1024, Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}}}
}

func TestNodeRadiusAdmissionReceiptCannotAcceptOriginalAttempt(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	client := NodeClient{ClientID: uuid.NewString(), Username: "alice", Password: "long-password"}
	descriptor := nodeAdmissionDescriptor(client.ClientID)
	broker := admission.New("native", func() (time.Time, error) { return time.Now(), nil })
	binding, _ := broker.Prepare([]admission.Descriptor{descriptor})
	auth := NewNodeAuthenticator(NodeConfiguration{Clients: []NodeClient{client}})
	if err := auth.BindAdmission(binding, descriptor.InboundTag); err != nil {
		t.Fatal(err)
	}
	if err := broker.Commit(7, binding); err != nil {
		t.Fatal(err)
	}
	for _, credentials := range [][2]string{{client.Username, "wrong-password"}, {"unknown", client.Password}} {
		verdict := auth.Authenticate(ctx, credentials[0], credentials[1], radius.Peer{})
		if verdict.Accept || verdict.Authenticated || verdict.Reason != "client authentication failed" {
			t.Fatalf("bad credentials were treated as authenticated demand=%+v", verdict)
		}
	}
	result := make(chan radius.Verdict, 1)
	go func() { result <- auth.Authenticate(ctx, client.Username, client.Password, radius.Peer{}) }()
	request, err := broker.Next(ctx)
	if err != nil || request.ClientId != client.ClientID || request.PlacementId != descriptor.PlacementID || request.InboundInstanceId != descriptor.InboundInstanceID || request.DeploymentRevision != 7 {
		t.Fatalf("RADIUS demand lost credential scope=%+v err=%v", request, err)
	}
	grant := nodeAdmissionGrant(client.ClientID)
	installed := make(chan struct{})
	go func() { auth.Grants([]governor.Grant{grant}); close(installed) }()
	select {
	case <-installed:
	case <-time.After(time.Second):
		t.Fatal("master wait held RADIUS authentication lock")
	}
	if err := broker.Complete(&nodev1.ActivationResult{RequestId: request.RequestId, ClientId: client.ClientID, TermId: grant.Policy.Term}); err != nil {
		t.Fatal(err)
	}
	if verdict := <-result; verdict.Accept || !verdict.Authenticated || verdict.Reason != "client authorization unavailable" {
		t.Fatalf("receipt accepted original RADIUS authentication=%+v", verdict)
	}
	if verdict := auth.Authenticate(ctx, client.Username, client.Password, radius.Peer{}); !verdict.Accept || !verdict.Authenticated {
		t.Fatalf("installed authority did not accept fresh authentication=%+v", verdict)
	}
	descriptor.PlacementRevision++
	next, _ := broker.Prepare([]admission.Descriptor{descriptor})
	if err := broker.Commit(8, next); err != nil {
		t.Fatal(err)
	}
	if verdict := auth.Authenticate(ctx, client.Username, client.Password, radius.Peer{}); verdict.Accept || auth.AuthenticationCurrent(client.Username) {
		t.Fatalf("retired RADIUS proof accepted funded client=%+v", verdict)
	}
	if err := auth.BindAdmission(next, descriptor.InboundTag); err != nil {
		t.Fatal(err)
	}
	if verdict := auth.Authenticate(ctx, client.Username, client.Password, radius.Peer{}); !verdict.Accept {
		t.Fatalf("fresh placement proof was not installed=%+v", verdict)
	}
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

func TestNodeRadiusPausedCredentialCannotUseReassignedAlias(t *testing.T) {
	client := NodeClient{ClientID: uuid.NewString(), Username: "alice", Password: "old-password"}
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
	result := make(chan radius.Verdict, 1)
	go func() { result <- auth.Authenticate(t.Context(), client.Username, client.Password, radius.Peer{}) }()
	<-clock.entered
	nextClient := client
	nextClient.ClientID, nextClient.Password = uuid.NewString(), "new-password"
	nextDescriptor := nodeAdmissionDescriptor(nextClient.ClientID)
	next, _ := broker.Prepare([]admission.Descriptor{nextDescriptor})
	if err := broker.Commit(2, next); err != nil {
		t.Fatal(err)
	}
	close(clock.release)
	if verdict := <-result; verdict.Accept || !verdict.Authenticated {
		t.Fatalf("paused RADIUS credential acquired alias authority=%+v", verdict)
	}
	fresh := NewNodeAuthenticator(NodeConfiguration{Clients: []NodeClient{nextClient}})
	if err := fresh.BindAdmission(next, nextDescriptor.InboundTag); err != nil {
		t.Fatal(err)
	}
	fresh.Grants([]governor.Grant{nodeAdmissionGrant(nextClient.ClientID)})
	if verdict := fresh.Authenticate(t.Context(), nextClient.Username, nextClient.Password, radius.Peer{}); !verdict.Accept {
		t.Fatalf("new RADIUS alias credential was refused=%+v", verdict)
	}
	if verdict := fresh.Authenticate(t.Context(), nextClient.Username, client.Password, radius.Peer{}); verdict.Accept || verdict.Authenticated {
		t.Fatalf("old RADIUS password was treated as authenticated=%+v", verdict)
	}
}
