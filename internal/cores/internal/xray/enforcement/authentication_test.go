package enforcement

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/xtls/xray-core/common/buf"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/session"

	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func TestXrayFundedAuthenticationCannotRebindRetiredPointer(t *testing.T) {
	first := admission.Descriptor{ClientID: uuid.NewString(), CredentialGeneration: 1, PlacementID: uuid.NewString(), PlacementRevision: 1, InboundInstanceID: uuid.NewString(), InboundTag: "hot"}
	broker := admission.New("xray", func() (time.Time, error) { return time.Now(), nil })
	registry := NewAuthentication()
	defer registry.Retire(nil)
	account, err := limits.New(limits.Policy{Term: "funded", Revision: 1, Enabled: true, CreditCeiling: 1024, Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := &guardedDispatcher{authentication: registry, lookup: func(string) (*limits.Account, bool) { return account, true }}
	bind := func(revision uint64, descriptor admission.Descriptor) (*protocol.MemoryUser, *admission.BoundProof) {
		t.Helper()
		binding, err := broker.Prepare([]admission.Descriptor{descriptor})
		if err != nil {
			t.Fatal(err)
		}
		proof, _ := binding.Capture(descriptor.ClientID, descriptor.InboundTag)
		user := &protocol.MemoryUser{Email: "reused-login"}
		if err := registry.Register(user, admission.Subject{ClientID: descriptor.ClientID, InboundTag: descriptor.InboundTag}, proof, true); err != nil {
			t.Fatal(err)
		}
		if err := broker.Commit(revision, binding); err != nil {
			t.Fatal(err)
		}
		return user, proof
	}
	old, _ := bind(1, first)
	oldContext := session.ContextWithInbound(t.Context(), &session.Inbound{Tag: "hot", User: old})
	_, client, authenticated, err := dispatcher.account(oldContext)
	if err != nil || client != first.ClientID {
		t.Fatalf("initial funded authentication=%q error=%v", client, err)
	}
	flow := context.WithValue(t.Context(), authenticatedFlowKey{}, authenticated)
	data := buf.MultiBuffer{buf.FromBytes([]byte("accepted"))}
	defer buf.ReleaseMulti(data)
	if n, err := authorize(flow, account, limits.Upload, data, false); err != nil || n != 8 {
		t.Fatalf("initial authority=%d error=%v", n, err)
	}
	next := first
	next.ClientID, next.PlacementID = uuid.NewString(), uuid.NewString()
	current, _ := bind(2, next)
	if _, _, _, err := dispatcher.account(oldContext); !errors.Is(err, limits.ErrBlocked) {
		t.Fatal("retired funded authentication was rebound by login")
	}
	if _, err := authorize(flow, account, limits.Upload, data, false); !errors.Is(err, limits.ErrBlocked) {
		t.Fatal("retired authentication forwarded with an existing funded account")
	}
	normalized := dispatcher.identityContext(oldContext, client, authenticated)
	if _, _, _, err := dispatcher.account(normalized); !errors.Is(err, limits.ErrBlocked) {
		t.Fatal("normalized old identity bypassed its retired proof")
	}
	ctx := session.ContextWithInbound(t.Context(), &session.Inbound{Tag: "hot", User: current})
	if _, client, _, err := dispatcher.account(ctx); err != nil || client != next.ClientID {
		t.Fatalf("fresh replacement authentication=%q error=%v", client, err)
	}
	registry.RetireUsers(current)
	if _, _, _, err := dispatcher.account(ctx); !errors.Is(err, limits.ErrBlocked) {
		t.Fatal("unregistered old pointer retained funded authority")
	}
	_, _ = bind(3, first)
	if _, _, _, err := dispatcher.account(oldContext); !errors.Is(err, limits.ErrBlocked) {
		t.Fatal("remove and re-add revived a retired pointer")
	}
}
