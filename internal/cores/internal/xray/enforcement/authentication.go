package enforcement

import (
	"context"
	"errors"
	"sync"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/transport"

	"github.com/Arman2122/p-ui/internal/node/admission"
)

type (
	authenticationKey    struct{}
	authenticatedFlowKey struct{}
)

type authentication struct {
	subject admission.Subject
	proof   *admission.BoundProof
	scoped  bool
	ctx     context.Context
	cancel  context.CancelFunc
}

func (a *authentication) current() bool {
	return a != nil && a.ctx.Err() == nil && (!a.scoped || a.proof.Current())
}

type Authentication struct {
	mu    sync.RWMutex
	users map[*protocol.MemoryUser]*authentication
}

func NewAuthentication() *Authentication {
	return &Authentication{users: make(map[*protocol.MemoryUser]*authentication)}
}

func WithAuthentication(ctx context.Context, registry *Authentication) context.Context {
	return context.WithValue(ctx, authenticationKey{}, registry)
}

func (r *Authentication) Register(user *protocol.MemoryUser, subject admission.Subject, proof *admission.BoundProof, scoped bool) error {
	if user == nil || subject.ClientID == "" || subject.InboundTag == "" || scoped && (proof == nil || proof.Descriptor().ClientID != subject.ClientID || proof.Descriptor().InboundTag != subject.InboundTag) {
		return admission.ErrUnavailable
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.users[user] != nil {
		return errors.New("Xray authenticated pointer is already registered")
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.users[user] = &authentication{subject: subject, proof: proof, scoped: scoped, ctx: ctx, cancel: cancel}
	return nil
}

func (r *Authentication) Matches(user *protocol.MemoryUser, subject admission.Subject, proof *admission.BoundProof, scoped bool) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a := r.users[user]
	return a != nil && a.ctx.Err() == nil && a.subject == subject && a.proof == proof && a.scoped == scoped
}

func (r *Authentication) Retire(subjects []admission.Subject) {
	changed := make(map[admission.Subject]bool, len(subjects))
	for _, subject := range subjects {
		changed[subject] = true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for user, record := range r.users {
		if subjects == nil || changed[record.subject] {
			record.cancel()
			delete(r.users, user)
		}
	}
}

func (r *Authentication) RetireUsers(users ...*protocol.MemoryUser) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, user := range users {
		if record := r.users[user]; record != nil {
			record.cancel()
			delete(r.users, user)
		}
	}
}

func (r *Authentication) resolve(user *protocol.MemoryUser, tag string) *authentication {
	if captured, ok := user.Account.(interface{ AuthenticatedUser() *protocol.MemoryUser }); ok && captured.AuthenticatedUser() != nil {
		user = captured.AuthenticatedUser()
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	record := r.users[user]
	if record == nil || record.subject.InboundTag != tag || !record.current() {
		return nil
	}
	return record
}

func (a *authentication) guard(ctx context.Context, link *transport.Link) context.Context {
	if a == nil {
		return ctx
	}
	ctx, cancel := context.WithCancel(ctx)
	ctx = context.WithValue(ctx, authenticatedFlowKey{}, a)
	stop := context.AfterFunc(a.ctx, func() {
		cancel()
		_ = common.Interrupt(link.Reader)
		_ = common.Interrupt(link.Writer)
	})
	context.AfterFunc(ctx, func() { stop() })
	return ctx
}
