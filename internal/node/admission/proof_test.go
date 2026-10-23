package admission

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
)

func TestHotProofCommitRetainsUnchangedAndPermanentlyRetiresReplacedCredentials(t *testing.T) {
	var trusted atomic.Bool
	trusted.Store(true)
	broker := New("ssh", func() (time.Time, error) {
		if !trusted.Load() {
			return time.Time{}, ErrUnavailable
		}
		return time.Now(), nil
	})
	a, b := testDescriptor(), testDescriptor()
	initial, err := broker.Prepare([]Descriptor{a, b})
	if err != nil {
		t.Fatal(err)
	}
	oldA, _ := initial.Capture(a.ClientID, a.InboundTag)
	oldB, _ := initial.Capture(b.ClientID, b.InboundTag)
	if oldA.Current() || oldB.Current() {
		t.Fatal("candidate proof became current before durable configuration commit")
	}
	if err := broker.Commit(1, initial); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- oldA.Request(t.Context()) }()
	request := nextAttempt(t, broker)
	rotated := a
	rotated.CredentialGeneration++
	next, err := broker.Prepare([]Descriptor{rotated, b})
	if err != nil {
		t.Fatal(err)
	}
	newA, _ := next.Capture(a.ClientID, a.InboundTag)
	newB, _ := next.Capture(b.ClientID, b.InboundTag)
	if oldA.Current() || newA.Current() || newB != oldB || !oldB.Current() {
		t.Fatal("hot prepare invalidated an unchanged identity or enabled a candidate")
	}
	if err := <-done; !errors.Is(err, ErrUnavailable) {
		t.Fatalf("retired pending request=%v", err)
	}
	if err := broker.Complete(&nodev1.ActivationResult{RequestId: request.RequestId, ClientId: a.ClientID}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("retired request accepted a late receipt")
	}
	if err := oldB.Request(t.Context()); !errors.Is(err, ErrUnavailable) {
		t.Fatal("pending configuration accepted fresh demand")
	}
	trusted.Store(false)
	if !oldB.Current() {
		t.Fatal("static unchanged proof depended on transient worker availability")
	}
	trusted.Store(true)
	if err := broker.Commit(2, next); err != nil {
		t.Fatal(err)
	}
	if !newA.Current() || oldA.Current() || !oldB.Current() {
		t.Fatal("committed proof ownership was not exact")
	}
	go func() { done <- newA.Request(t.Context()) }()
	request = nextAttempt(t, broker)
	if request.CredentialGeneration != rotated.CredentialGeneration || request.DeploymentRevision != 2 {
		t.Fatal("new authentication did not use the committed descriptor and base")
	}
	if err := broker.Complete(&nodev1.ActivationResult{RequestId: request.RequestId, ClientId: a.ClientID}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrUnavailable) {
		t.Fatal("receipt granted authority")
	}
	failed, err := broker.Prepare([]Descriptor{b})
	if err != nil {
		t.Fatal(err)
	}
	aba, err := broker.Prepare([]Descriptor{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if err := broker.Commit(3, failed); !errors.Is(err, ErrUnavailable) {
		t.Fatal("superseded candidate was committed")
	}
	if err := broker.Commit(4, aba); err != nil {
		t.Fatal(err)
	}
	if oldA.Current() || newA.Current() || !oldB.Current() {
		t.Fatal("failed apply or ABA revived a retired authentication proof")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if err := oldA.Request(ctx); !errors.Is(err, ErrUnavailable) {
		t.Fatal("retired proof queued after ABA")
	}
	if _, err := broker.Next(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("retired proof escaped the bounded queue: %v", err)
	}
}
