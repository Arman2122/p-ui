package limits

import (
	"context"
	"io"
	"net"
	"sync"
)

type Conn struct {
	net.Conn
	ctx     context.Context
	cancel  context.CancelFunc
	account *Account
	readMu  sync.Mutex
	writeMu sync.Mutex
	pending []byte
	readErr error
}

// WrapConn deliberately hides zero-copy and unwrapping interfaces.
// Every forwarded chunk must pass through the shared admission account.
func WrapConn(ctx context.Context, conn net.Conn, account *Account) *Conn {
	ctx, cancel := context.WithCancel(ctx)
	return &Conn{Conn: conn, ctx: ctx, cancel: cancel, account: account}
}

func (c *Conn) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.readMu.Lock()
	defer c.readMu.Unlock()
	var n int
	if len(c.pending) > 0 {
		n = copy(p, c.pending)
		c.pending = c.pending[n:]
	} else {
		if c.readErr != nil {
			return 0, c.readErr
		}
		n, c.readErr = c.Conn.Read(p)
	}
	if n == 0 {
		return 0, c.readErr
	}
	allowed, err := c.account.TakeUpTo(c.ctx, Upload, n)
	if err != nil {
		return 0, err
	}
	if allowed < n {
		remaining := append([]byte(nil), p[allowed:n]...)
		c.pending = append(remaining, c.pending...)
	}
	c.account.Record(Upload, allowed)
	return allowed, nil
}

func (c *Conn) Write(p []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	total := 0
	for len(p) > 0 {
		allowed, err := c.account.TakeUpTo(c.ctx, Download, len(p))
		if err != nil {
			return total, err
		}
		n, err := c.Conn.Write(p[:allowed])
		c.account.Record(Download, n)
		total += n
		p = p[n:]
		if err != nil {
			return total, err
		}
		if n != allowed {
			return total, io.ErrShortWrite
		}
	}
	return total, nil
}

func (c *Conn) Close() error { c.cancel(); return c.Conn.Close() }
