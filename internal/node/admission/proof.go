package admission

import (
	"context"
	"math"
)

type Subject struct {
	ClientID   string
	InboundTag string
}

type BoundProof struct {
	broker     *Broker
	descriptor Descriptor
}

func (p *BoundProof) Descriptor() Descriptor { return p.descriptor }

func (p *BoundProof) Current() bool {
	if p == nil {
		return false
	}
	p.broker.mu.Lock()
	defer p.broker.mu.Unlock()
	return p.broker.proofs[descriptorKey(p.descriptor)] == p
}

func (p *BoundProof) Request(ctx context.Context) error {
	if p == nil {
		return ErrUnavailable
	}
	return p.broker.requestProof(ctx, p.descriptor, p)
}

type Binding struct {
	broker *Broker
	epoch  uint64
	proofs map[string]*BoundProof
}

func (b *Binding) Capture(client, inbound string) (*BoundProof, bool) {
	if b == nil {
		return nil, false
	}
	proof, ok := b.proofs[inbound+"\x00"+client]
	return proof, ok
}

func (b *Binding) Handler(ctx context.Context, client, inbound string) error {
	proof, _ := b.Capture(client, inbound)
	return proof.Request(ctx)
}

type bindingKey struct{}

func WithBinding(ctx context.Context, binding *Binding) context.Context {
	return WithHandler(context.WithValue(ctx, bindingKey{}, binding), binding.Handler)
}

func BindingFromContext(ctx context.Context) *Binding {
	binding, _ := ctx.Value(bindingKey{}).(*Binding)
	return binding
}

func descriptorKey(descriptor Descriptor) string {
	return descriptor.InboundTag + "\x00" + descriptor.ClientID
}

// Prepare retains unchanged proof objects while permanently detaching changed ones.
// New proofs become current only after the worker durably acknowledges the configuration.
func (b *Broker) Prepare(descriptors []Descriptor) (*Binding, error) {
	if err := Validate(descriptors); err != nil {
		return nil, err
	}
	desired := make(map[string]*BoundProof, len(descriptors))
	for _, descriptor := range descriptors {
		desired[descriptorKey(descriptor)] = &BoundProof{broker: b, descriptor: descriptor}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	retained := make(map[string]*BoundProof, len(b.proofs))
	values := make(map[string]Descriptor, len(b.descriptors))
	for key, proof := range b.proofs {
		if next := desired[key]; next != nil && next.descriptor == proof.descriptor {
			desired[key], retained[key], values[key] = proof, proof, proof.descriptor
		}
	}
	b.cancelPending()
	b.epoch++
	b.preparing = true
	b.proofs, b.descriptors = retained, values
	return &Binding{broker: b, epoch: b.epoch, proofs: desired}, nil
}

func (b *Broker) Commit(revision uint64, binding *Binding) error {
	if binding == nil || binding.broker != b || revision > math.MaxInt64 || len(binding.proofs) != 0 && revision == 0 {
		return ErrUnavailable
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if binding.epoch != b.epoch || !b.preparing {
		return ErrUnavailable
	}
	values := make(map[string]Descriptor, len(binding.proofs))
	for key, proof := range binding.proofs {
		values[key] = proof.descriptor
	}
	b.proofs, b.descriptors = binding.proofs, values
	b.revision, b.preparing = revision, false
	b.signal()
	return nil
}

func (b *Broker) cancelPending() {
	for _, pending := range b.pending {
		close(pending.done)
	}
	b.pending = make(map[string]*attempt)
	b.clients = make(map[string]bool)
	b.signal()
}
