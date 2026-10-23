package enforcement

import (
	"context"
	"io"
	"sync"

	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/buf"

	"github.com/Arman2122/p-ui/internal/node/limits"
)

type writer struct {
	ctx     context.Context
	base    buf.Writer
	account *limits.Account
	dir     limits.Direction
	packet  bool
}

func authorize(ctx context.Context, account *limits.Account, dir limits.Direction, data buf.MultiBuffer, packet bool) (int, error) {
	if authenticated, _ := ctx.Value(authenticatedFlowKey{}).(*authentication); authenticated != nil && !authenticated.current() {
		return 0, limits.ErrBlocked
	}
	if packet || data[0].UDP != nil {
		n := int(data[0].Len())
		return n, account.Take(ctx, dir, n)
	}
	return account.TakeUpTo(ctx, dir, min(int(data[0].Len()), int(buf.Size)))
}

func split(data buf.MultiBuffer, n int, packet bool) (buf.MultiBuffer, buf.MultiBuffer) {
	if packet || data[0].UDP != nil {
		rest, first := buf.SplitFirst(data)
		return rest, buf.MultiBuffer{first}
	}
	return buf.SplitSize(data, int32(n))
}

func (w *writer) WriteMultiBuffer(data buf.MultiBuffer) error {
	for len(data) > 0 {
		if data[0].IsEmpty() {
			data[0].Release()
			data = data[1:]
			continue
		}
		n, err := authorize(w.ctx, w.account, w.dir, data, w.packet)
		if err != nil {
			buf.ReleaseMulti(data)
			return err
		}
		rest, part := split(data, n, w.packet)
		data = rest
		if err := w.base.WriteMultiBuffer(part); err != nil {
			buf.ReleaseMulti(data)
			return err
		}
		w.account.Record(w.dir, n)
	}
	return nil
}

func (w *writer) Close() error { return common.Close(w.base) }
func (w *writer) Interrupt()   { _ = common.Interrupt(w.base) }

type reader struct {
	ctx      context.Context
	cancel   context.CancelFunc
	base     buf.Reader
	account  *limits.Account
	dir      limits.Direction
	packet   bool
	mu       sync.Mutex
	pending  buf.MultiBuffer
	terminal error
}

func newReader(ctx context.Context, base buf.Reader, account *limits.Account, dir limits.Direction, packet bool) *reader {
	ctx, cancel := context.WithCancel(ctx)
	return &reader{ctx: ctx, cancel: cancel, base: base, account: account, dir: dir, packet: packet}
}

func (r *reader) ReadMultiBuffer() (buf.MultiBuffer, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for len(r.pending) == 0 {
		if r.terminal != nil {
			return nil, r.terminal
		}
		if err := r.ctx.Err(); err != nil {
			return nil, err
		}
		r.pending, r.terminal = r.base.ReadMultiBuffer()
	}
	for len(r.pending) > 0 && r.pending[0].IsEmpty() {
		r.pending[0].Release()
		r.pending = r.pending[1:]
	}
	if len(r.pending) == 0 {
		return nil, nil
	}
	n, err := authorize(r.ctx, r.account, r.dir, r.pending, r.packet)
	if err != nil {
		r.pending = buf.ReleaseMulti(r.pending)
		r.terminal = err
		return nil, err
	}
	var part buf.MultiBuffer
	r.pending, part = split(r.pending, n, r.packet)
	r.account.Record(r.dir, n)
	return part, nil
}

func (r *reader) Interrupt() {
	r.cancel()
	_ = common.Interrupt(r.base)
	r.mu.Lock()
	r.pending = buf.ReleaseMulti(r.pending)
	r.terminal = io.ErrClosedPipe
	r.mu.Unlock()
}

func (r *reader) Close() error { r.Interrupt(); return common.Close(r.base) }
