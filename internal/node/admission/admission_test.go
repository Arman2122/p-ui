package admission

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
)

func testDescriptor() Descriptor {
	return Descriptor{ClientID: uuid.NewString(), CredentialGeneration: 7, PlacementID: uuid.NewString(), PlacementRevision: 11, InboundInstanceID: uuid.NewString(), InboundTag: "inbound-example"}
}

func nextAttempt(t *testing.T, broker *Broker) *nodev1.ActivationRequest {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	request, err := broker.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func TestAuthenticatedDemandIsEphemeralScopedAndNeverCredit(t *testing.T) {
	var clock atomic.Int64
	clock.Store(1_800_000_000_000)
	broker := New("xray", func() (time.Time, error) { return time.UnixMilli(clock.Load()), nil })
	descriptor := testDescriptor()
	handler, err := broker.Bind([]Descriptor{descriptor})
	if err != nil {
		t.Fatal(err)
	}
	if err := broker.Replace(19, []Descriptor{descriptor}); err != nil {
		t.Fatal(err)
	}
	for _, input := range [][2]string{{uuid.NewString(), descriptor.InboundTag}, {descriptor.ClientID, "foreign-inbound"}} {
		if err := handler(t.Context(), input[0], input[1]); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("unassigned demand=%v", err)
		}
	}
	done := make(chan error, 1)
	go func() { done <- handler(t.Context(), descriptor.ClientID, descriptor.InboundTag) }()
	request := nextAttempt(t, broker)
	if request.RuntimeId != "xray" || request.DeploymentRevision != 19 || request.PlacementId != descriptor.PlacementID || request.PlacementRevision != 11 ||
		request.ClientId != descriptor.ClientID || request.CredentialGeneration != 7 || request.InboundInstanceId != descriptor.InboundInstanceID ||
		request.DeadlineUnixMilli <= clock.Load() || request.DeadlineUnixMilli > clock.Load()+5000 {
		t.Fatalf("incorrect request scope: %+v", request)
	}
	if err := handler(t.Context(), descriptor.ClientID, descriptor.InboundTag); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("duplicate demand=%v", err)
	}
	replay, cancelReplay := context.WithTimeout(t.Context(), 10*time.Millisecond)
	if repeated, err := broker.Next(replay); repeated != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("delivered demand replay=%v %v", repeated, err)
	}
	cancelReplay()
	result := &nodev1.ActivationResult{RequestId: request.RequestId, ClientId: uuid.NewString(), TermId: uuid.NewString()}
	if err := broker.Complete(result); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("foreign receipt=%v", err)
	}
	result.ClientId = descriptor.ClientID
	if err := broker.Complete(result); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrUnavailable) {
		t.Fatalf("term receipt granted forwarding: %v", err)
	}
	if err := broker.Complete(result); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("replayed receipt=%v", err)
	}
	go func() { done <- handler(t.Context(), descriptor.ClientID, descriptor.InboundTag) }()
	request = nextAttempt(t, broker)
	clock.Add(6000)
	if err := broker.Complete(&nodev1.ActivationResult{RequestId: request.RequestId, ClientId: descriptor.ClientID}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("expired receipt=%v", err)
	}
	if err := broker.Replace(20, nil); err != nil {
		t.Fatal(err)
	}
	if err := <-done; !errors.Is(err, ErrUnavailable) {
		t.Fatalf("retired attempt=%v", err)
	}
}

func TestCanceledDemandIsNotReplayedAfterReconnect(t *testing.T) {
	broker := New("xray", func() (time.Time, error) { return time.Now(), nil })
	descriptor := testDescriptor()
	handler, err := broker.Bind([]Descriptor{descriptor})
	if err != nil {
		t.Fatal(err)
	}
	if err := broker.Replace(1, []Descriptor{descriptor}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- handler(ctx, descriptor.ClientID, descriptor.InboundTag) }()
	request := nextAttempt(t, broker)
	cancel()
	if err := <-done; !errors.Is(err, ErrUnavailable) {
		t.Fatalf("canceled demand=%v", err)
	}
	if err := broker.Complete(&nodev1.ActivationResult{RequestId: request.RequestId, ClientId: descriptor.ClientID}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("canceled receipt=%v", err)
	}
	ctx, cancel = context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer cancel()
	if request, err := broker.Next(ctx); request != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("stale demand replay=%v %v", request, err)
	}
}

func TestDemandCapacityAndUnknownTimeFailClosed(t *testing.T) {
	var trusted atomic.Bool
	broker := New("xray", func() (time.Time, error) {
		if !trusted.Load() {
			return time.Time{}, ErrUnavailable
		}
		return time.Now(), nil
	})
	descriptors := make([]Descriptor, MaxPending+1)
	for i := range descriptors {
		descriptors[i] = testDescriptor()
	}
	handler, err := broker.Bind(descriptors)
	if err != nil {
		t.Fatal(err)
	}
	if err := broker.Replace(1, descriptors); err != nil {
		t.Fatal(err)
	}
	if err := handler(t.Context(), descriptors[0].ClientID, descriptors[0].InboundTag); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	trusted.Store(true)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var wg sync.WaitGroup
	for _, descriptor := range descriptors[:MaxPending] {
		wg.Go(func() { _ = handler(ctx, descriptor.ClientID, descriptor.InboundTag) })
		_ = nextAttempt(t, broker)
	}
	last := descriptors[MaxPending]
	overflow := make(chan error, 1)
	go func() { overflow <- handler(ctx, last.ClientID, last.InboundTag) }()
	select {
	case err := <-overflow:
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("capacity bypass=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("overflow authentication retained instead of rejected")
	}
	cancel()
	wg.Wait()
	broker.mu.Lock()
	defer broker.mu.Unlock()
	if len(broker.pending) != 0 || len(broker.clients) != 0 {
		t.Fatal("canceled attempts retained")
	}
}
