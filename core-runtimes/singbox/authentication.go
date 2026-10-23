package singbox

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"sync"

	anytls "github.com/anytls/sing-anytls"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/auth"
	"github.com/sagernet/sing/common/bufio"

	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

type authenticationKey struct{}

type authenticatedUser struct {
	subject admission.Subject
	proof   *admission.BoundProof
	scoped  bool
	ctx     context.Context
	cancel  context.CancelFunc
}

func (a *authenticatedUser) current() bool {
	return a != nil && a.ctx.Err() == nil && (!a.scoped || a.proof.Current())
}

type authenticationRegistry struct {
	mu    sync.RWMutex
	users map[any]*authenticatedUser
}

func newAuthenticationRegistry() *authenticationRegistry {
	return &authenticationRegistry{users: make(map[any]*authenticatedUser)}
}

func authenticationName(handle any) string {
	switch user := handle.(type) {
	case *option.TUICUser:
		return user.Name
	case *anytls.User:
		return user.Name
	default:
		return ""
	}
}

func sameAuthentication(a, b any) bool {
	switch a := a.(type) {
	case *option.TUICUser:
		other, ok := b.(*option.TUICUser)
		return ok && *a == *other
	case *anytls.User:
		other, ok := b.(*anytls.User)
		return ok && *a == *other
	default:
		return false
	}
}

func (r *authenticationRegistry) resolve(ctx context.Context, tag string) *authenticatedUser {
	var handle any
	if user, ok := auth.UserFromContext[*option.TUICUser](ctx); ok {
		handle = user
	}
	if user, ok := auth.UserFromContext[*anytls.User](ctx); ok {
		handle = user
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	record := r.users[handle]
	if record == nil || record.subject.InboundTag != tag || !record.current() {
		return nil
	}
	return record
}

func (r *authenticationRegistry) retire(handles ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, handle := range handles {
		if record := r.users[handle]; record != nil {
			record.cancel()
			delete(r.users, handle)
		}
	}
}

func (r *authenticationRegistry) invalidate(subjects []admission.Subject) {
	changed := make(map[admission.Subject]bool, len(subjects))
	for _, subject := range subjects {
		changed[subject] = true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for handle, record := range r.users {
		if subjects == nil || changed[record.subject] {
			record.cancel()
			delete(r.users, handle)
		}
	}
}

func (r *authenticationRegistry) reconcile(ctx context.Context, tag string, before, desired []any, initial bool) ([]any, error) {
	binding := admission.BindingFromContext(ctx)
	wanted := make(map[string]any, len(desired))
	proofs := make(map[string]*admission.BoundProof, len(desired))
	for _, handle := range desired {
		name := authenticationName(handle)
		proof, found := binding.Capture(name, tag)
		if binding != nil && !found {
			return nil, admission.ErrUnavailable
		}
		wanted[name], proofs[name] = handle, proof
	}
	previous := make(map[string]any, len(before))
	for _, handle := range before {
		previous[authenticationName(handle)] = handle
	}
	var next []any
	r.mu.Lock()
	defer r.mu.Unlock()
	for name, handle := range wanted {
		proof := proofs[name]
		subject := admission.Subject{ClientID: name, InboundTag: tag}
		old := previous[name]
		record := r.users[old]
		if old != nil && sameAuthentication(old, handle) && (initial || record != nil && record.ctx.Err() == nil && record.subject == subject && record.proof == proof && record.scoped == (binding != nil)) {
			handle = old
		}
		if initial && (old == nil || !sameAuthentication(old, handle)) {
			return nil, errors.New("sing-box authentication missing before start")
		}
		if r.users[handle] == nil {
			lifetime, cancel := context.WithCancel(context.Background())
			r.users[handle] = &authenticatedUser{subject: subject, proof: proof, scoped: binding != nil, ctx: lifetime, cancel: cancel}
		}
		if old != handle && record != nil {
			record.cancel()
			delete(r.users, old)
		}
		next = append(next, handle)
		delete(previous, name)
	}
	for _, handle := range previous {
		if record := r.users[handle]; record != nil {
			record.cancel()
			delete(r.users, handle)
		}
	}
	return next, nil
}

func (a *authenticatedUser) guard(ctx context.Context, close func() error) (context.Context, func()) {
	if a == nil {
		return ctx, func() {}
	}
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(a.ctx, func() { cancel(); _ = close() })
	stopContext := context.AfterFunc(ctx, func() { stop() })
	return ctx, func() { stop(); stopContext(); cancel() }
}

type authenticatedConn struct {
	net.Conn
	authentication *authenticatedUser
	stop           func()
}

type cachedAuthenticationConn struct {
	net.Conn
	reader io.Reader
}

func (c *cachedAuthenticationConn) Read(data []byte) (int, error) { return c.reader.Read(data) }

func ownAuthenticationCache(conn net.Conn) net.Conn {
	var readers []io.Reader
	for {
		cached, ok := conn.(*bufio.CachedConn)
		if !ok {
			break
		}
		if data := cached.ReadCached(); data != nil {
			readers = append(readers, bytes.NewReader(bytes.Clone(data.Bytes())))
			data.Release()
		}
		conn = cached.Conn
	}
	if len(readers) == 0 {
		return conn
	}
	readers = append(readers, conn)
	return &cachedAuthenticationConn{Conn: conn, reader: io.MultiReader(readers...)}
}

func (c *authenticatedConn) Read(data []byte) (int, error) {
	if !c.authentication.current() {
		return 0, limits.ErrBlocked
	}
	n, err := c.Conn.Read(data)
	if !c.authentication.current() {
		return 0, limits.ErrBlocked
	}
	return n, err
}

func (c *authenticatedConn) Write(data []byte) (int, error) {
	total := 0
	for len(data) > 0 {
		if !c.authentication.current() {
			return total, limits.ErrBlocked
		}
		n, err := c.Conn.Write(data[:min(len(data), 2048)])
		total += n
		data = data[n:]
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

func (c *authenticatedConn) Close() error { c.stop(); return c.Conn.Close() }
