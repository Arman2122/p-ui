package admission

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
)

var ErrUnavailable = errors.New("fresh client authorization unavailable")

const MaxPending = 256

type Descriptor struct {
	ClientID             string `json:"clientId"`
	CredentialGeneration uint64 `json:"credentialGeneration"`
	PlacementID          string `json:"placementId"`
	PlacementRevision    uint64 `json:"placementRevision"`
	InboundInstanceID    string `json:"inboundInstanceId"`
	InboundTag           string `json:"inboundTag"`
}

type (
	Handler    func(context.Context, string, string) error
	handlerKey struct{}
)

func WithHandler(ctx context.Context, handler Handler) context.Context {
	return context.WithValue(ctx, handlerKey{}, handler)
}

func FromContext(ctx context.Context) Handler {
	handler, _ := ctx.Value(handlerKey{}).(Handler)
	return handler
}

func ValidID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func Validate(descriptors []Descriptor) error {
	if len(descriptors) > 1_000_000 {
		return ErrUnavailable
	}
	seen := make(map[string]bool, len(descriptors))
	for _, descriptor := range descriptors {
		key := descriptor.InboundTag + "\x00" + descriptor.ClientID
		if !ValidID(descriptor.ClientID) || !ValidID(descriptor.PlacementID) || !ValidID(descriptor.InboundInstanceID) ||
			descriptor.CredentialGeneration == 0 || descriptor.CredentialGeneration > math.MaxInt64 || descriptor.PlacementRevision == 0 || descriptor.PlacementRevision > math.MaxInt64 ||
			descriptor.InboundTag == "" || len(descriptor.InboundTag) > 128 || strings.ContainsRune(descriptor.InboundTag, 0) || seen[key] {
			return ErrUnavailable
		}
		seen[key] = true
	}
	return nil
}

type attempt struct {
	request   *nodev1.ActivationRequest
	key       string
	ctx       context.Context
	done      chan struct{}
	delivered bool
}

type Broker struct {
	mu          sync.Mutex
	runtime     string
	revision    uint64
	descriptors map[string]Descriptor
	proofs      map[string]*BoundProof
	epoch       uint64
	preparing   bool
	pending     map[string]*attempt
	clients     map[string]bool
	changed     chan struct{}
	now         func() (time.Time, error)
}

func New(runtime string, now func() (time.Time, error)) *Broker {
	return &Broker{runtime: runtime, now: now, pending: make(map[string]*attempt), clients: make(map[string]bool), changed: make(chan struct{})}
}

func (b *Broker) Replace(revision uint64, descriptors []Descriptor) error {
	if revision > math.MaxInt64 || len(descriptors) != 0 && revision == 0 || Validate(descriptors) != nil {
		return ErrUnavailable
	}
	values := make(map[string]Descriptor, len(descriptors))
	for _, descriptor := range descriptors {
		values[descriptor.InboundTag+"\x00"+descriptor.ClientID] = descriptor
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	proofs := make(map[string]*BoundProof, len(values))
	for key, descriptor := range values {
		proof := b.proofs[key]
		if proof == nil || proof.descriptor != descriptor {
			proof = &BoundProof{broker: b, descriptor: descriptor}
		}
		proofs[key] = proof
	}
	b.cancelPending()
	b.epoch++
	b.proofs, b.preparing = proofs, false
	b.revision, b.descriptors = revision, values
	return nil
}

func (b *Broker) signal() {
	close(b.changed)
	b.changed = make(chan struct{})
}

func (b *Broker) Bind(descriptors []Descriptor) (Handler, error) {
	if err := Validate(descriptors); err != nil {
		return nil, err
	}
	proofs := make(map[string]Descriptor, len(descriptors))
	for _, descriptor := range descriptors {
		proofs[descriptor.InboundTag+"\x00"+descriptor.ClientID] = descriptor
	}
	return func(ctx context.Context, client, inbound string) error {
		expected, ok := proofs[inbound+"\x00"+client]
		if !ok {
			return ErrUnavailable
		}
		return b.request(ctx, expected)
	}, nil
}

// A receipt wakes the authenticated attempt; it never supplies forwarding authority.
func (b *Broker) request(ctx context.Context, expected Descriptor) error {
	return b.requestProof(ctx, expected, nil)
}

func (b *Broker) requestProof(ctx context.Context, expected Descriptor, proof *BoundProof) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	b.mu.Lock()
	client := expected.ClientID
	key := expected.InboundTag + "\x00" + client
	descriptor, ok := b.descriptors[key]
	if !ok || descriptor != expected || proof != nil && b.proofs[key] != proof || b.preparing || b.now == nil || ctx.Err() != nil || len(b.pending) >= MaxPending || b.clients[client] {
		b.mu.Unlock()
		return ErrUnavailable
	}
	now, err := b.now()
	if err != nil {
		b.mu.Unlock()
		return ErrUnavailable
	}
	request := &nodev1.ActivationRequest{
		RequestId: uuid.NewString(), ClientId: client, CredentialGeneration: descriptor.CredentialGeneration,
		DeadlineUnixMilli: now.Add(5 * time.Second).UnixMilli(), RuntimeId: b.runtime, DeploymentRevision: b.revision,
		PlacementId: descriptor.PlacementID, PlacementRevision: descriptor.PlacementRevision, InboundInstanceId: descriptor.InboundInstanceID,
	}
	if deadline, ok := ctx.Deadline(); ok {
		request.DeadlineUnixMilli = now.Add(max(0, time.Until(deadline))).UnixMilli()
	}
	pending := &attempt{request: request, key: client, ctx: ctx, done: make(chan struct{})}
	b.pending[request.RequestId], b.clients[client] = pending, true
	b.signal()
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		if b.pending[request.RequestId] == pending {
			delete(b.pending, request.RequestId)
			delete(b.clients, client)
		}
		b.mu.Unlock()
	}()
	select {
	case <-ctx.Done():
	case <-pending.done:
	}
	return ErrUnavailable
}

func (b *Broker) Next(ctx context.Context) (*nodev1.ActivationRequest, error) {
	for {
		b.mu.Lock()
		if ctx.Err() != nil {
			b.mu.Unlock()
			return nil, ctx.Err()
		}
		for _, pending := range b.pending {
			if pending.delivered || pending.ctx.Err() != nil {
				continue
			}
			now, err := b.now()
			if err != nil || now.UnixMilli() >= pending.request.DeadlineUnixMilli {
				continue
			}
			pending.delivered = true
			request := proto.Clone(pending.request).(*nodev1.ActivationRequest)
			b.mu.Unlock()
			return request, nil
		}
		changed := b.changed
		b.mu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-changed:
		}
	}
}

func (b *Broker) Complete(result *nodev1.ActivationResult) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if result == nil {
		return ErrUnavailable
	}
	pending := b.pending[result.RequestId]
	if pending == nil || !pending.delivered || pending.ctx.Err() != nil || result.ClientId != pending.request.ClientId {
		return ErrUnavailable
	}
	now, err := b.now()
	if err != nil || now.UnixMilli() >= pending.request.DeadlineUnixMilli {
		return ErrUnavailable
	}
	delete(b.pending, result.RequestId)
	delete(b.clients, pending.key)
	close(pending.done)
	return nil
}
