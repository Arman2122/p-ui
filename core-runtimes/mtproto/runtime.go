package mtproto

import (
	"context"
	"errors"
	"io"

	"github.com/Arman2122/mtg-proxy/essentials"
	"github.com/Arman2122/mtg-proxy/mtglib"

	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

type Lookup func(string) (*limits.Account, bool)

func New(options mtglib.ProxyOpts, lookup Lookup) (*mtglib.Proxy, error) {
	return newWithDemand(options, lookup, nil, "")
}

func newWithDemand(options mtglib.ProxyOpts, lookup Lookup, demand admission.Handler, inbound string) (*mtglib.Proxy, error) {
	if lookup == nil {
		return nil, limits.ErrBlocked
	}
	options.GovernStream = func(ctx context.Context, name string, proof mtglib.Authentication, connection essentials.Conn, stop func()) (essentials.Conn, error) {
		if proof != nil && !proof.Current() {
			return nil, limits.ErrBlocked
		}
		account, ok := lookup(name)
		if !ok || account == nil {
			if proof != nil {
				_ = proof.Request(ctx)
			} else if demand != nil {
				_ = demand(ctx, name, inbound)
			}
			return nil, limits.ErrBlocked
		}
		if err := account.Check(); err != nil {
			if proof != nil {
				_ = proof.Request(ctx)
			} else if demand != nil {
				_ = demand(ctx, name, inbound)
			}
			return nil, err
		}
		return &governedConn{Conn: limits.WrapConn(ctx, connection, account), base: connection, stop: stop}, nil
	}
	return mtglib.NewProxy(options)
}

type governedConn struct {
	*limits.Conn
	base essentials.Conn
	stop func()
}

func (c *governedConn) Read(data []byte) (int, error) {
	n, err := c.Conn.Read(data)
	if err != nil && !errors.Is(err, io.EOF) {
		c.stop()
	}
	return n, err
}

func (c *governedConn) Write(data []byte) (int, error) {
	n, err := c.Conn.Write(data)
	if err != nil {
		c.stop()
	}
	return n, err
}

func (c *governedConn) CloseRead() error  { return c.base.CloseRead() }
func (c *governedConn) CloseWrite() error { return c.base.CloseWrite() }
