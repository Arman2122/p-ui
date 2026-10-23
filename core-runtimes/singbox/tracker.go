package singbox

import (
	"context"
	"net"

	"github.com/sagernet/sing-box/adapter"
	tun "github.com/sagernet/sing-tun"
	"github.com/sagernet/sing/common/buf"
	M "github.com/sagernet/sing/common/metadata"
	N "github.com/sagernet/sing/common/network"

	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

type (
	Lookup  func(client string) (*limits.Account, bool)
	Tracker struct {
		Lookup         Lookup
		Demand         admission.Handler
		authentication *authenticationRegistry
	}
)

func (t *Tracker) account(ctx context.Context, user, inbound string) (*limits.Account, *authenticatedUser, error) {
	var authenticated *authenticatedUser
	if t.authentication != nil {
		authenticated = t.authentication.resolve(ctx, inbound)
		if !authenticated.current() {
			return nil, nil, limits.ErrBlocked
		}
		user = authenticated.subject.ClientID
	}
	if user == "" || t.Lookup == nil {
		return nil, nil, limits.ErrBlocked
	}
	a, ok := t.Lookup(user)
	if !ok || a == nil {
		t.request(ctx, authenticated, user, inbound)
		return nil, nil, limits.ErrBlocked
	}
	if err := a.Check(); err != nil {
		t.request(ctx, authenticated, user, inbound)
		return nil, nil, err
	}
	return a, authenticated, nil
}

func (t *Tracker) request(ctx context.Context, authenticated *authenticatedUser, user, inbound string) {
	if authenticated != nil && authenticated.scoped {
		_ = authenticated.proof.Request(ctx)
	} else if t.Demand != nil {
		_ = t.Demand(ctx, user, inbound)
	}
}

func (t *Tracker) RoutedConnection(ctx context.Context, c net.Conn, m adapter.InboundContext, _ adapter.Rule, _ adapter.Outbound) net.Conn {
	a, authenticated, err := t.account(ctx, m.User, m.Inbound)
	if err != nil {
		_ = c.Close()
		return &deniedConn{Conn: c, err: err}
	}
	c = ownAuthenticationCache(c)
	ctx, stop := authenticated.guard(ctx, c.Close)
	connection := limits.WrapConn(ctx, c, a)
	if authenticated == nil {
		return connection
	}
	return &authenticatedConn{Conn: connection, authentication: authenticated, stop: stop}
}

func (t *Tracker) RoutedPacketConnection(ctx context.Context, c N.PacketConn, m adapter.InboundContext, _ adapter.Rule, _ adapter.Outbound) N.PacketConn {
	a, authenticated, err := t.account(ctx, m.User, m.Inbound)
	if err != nil {
		_ = c.Close()
		return &packetConn{PacketConn: c, err: err}
	}
	ctx, stop := authenticated.guard(ctx, c.Close)
	return &packetConn{PacketConn: c, ctx: ctx, account: a, authentication: authenticated, stop: stop}
}

// TUIC and AnyTLS use routed stream/packet connections, never kernel flow offload.
func (t *Tracker) RoutedFlow(context.Context, adapter.InboundContext, adapter.Rule, adapter.Outbound) tun.FlowTracker {
	return nil
}

type deniedConn struct {
	net.Conn
	err error
}

func (c *deniedConn) Read([]byte) (int, error)  { return 0, c.err }
func (c *deniedConn) Write([]byte) (int, error) { return 0, c.err }

type packetConn struct {
	N.PacketConn
	ctx            context.Context
	account        *limits.Account
	err            error
	authentication *authenticatedUser
	stop           func()
}

func (c *packetConn) ReadPacket(b *buf.Buffer) (M.Socksaddr, error) {
	if c.err != nil {
		return M.Socksaddr{}, c.err
	}
	dest, err := c.PacketConn.ReadPacket(b)
	if err != nil {
		return dest, err
	}
	if c.authentication != nil && !c.authentication.current() {
		b.Truncate(0)
		return M.Socksaddr{}, limits.ErrBlocked
	}
	if b.Len() == 0 {
		return dest, nil
	}
	if err := c.account.Take(c.ctx, limits.Upload, b.Len()); err != nil {
		b.Truncate(0)
		return M.Socksaddr{}, err
	}
	c.account.Record(limits.Upload, b.Len())
	return dest, nil
}

func (c *packetConn) WritePacket(b *buf.Buffer, dest M.Socksaddr) error {
	if c.err != nil {
		b.Release()
		return c.err
	}
	if c.authentication != nil && !c.authentication.current() {
		b.Release()
		return limits.ErrBlocked
	}
	if b.Len() != 0 {
		if err := c.account.Take(c.ctx, limits.Download, b.Len()); err != nil {
			b.Release()
			return err
		}
	}
	n := b.Len()
	if err := c.PacketConn.WritePacket(b, dest); err != nil {
		return err
	}
	c.account.Record(limits.Download, n)
	return nil
}

func (c *packetConn) Close() error {
	if c.stop != nil {
		c.stop()
	}
	return c.PacketConn.Close()
}
