package enforcement

import (
	"context"
	"errors"
	"fmt"

	"github.com/sagernet/sing-shadowsocks/shadowaead_2022"
	"github.com/xtls/xray-core/app/dispatcher"
	"github.com/xtls/xray-core/common"
	"github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/session"
	"github.com/xtls/xray-core/common/singbridge"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/features/routing"
	"github.com/xtls/xray-core/transport"

	"github.com/Arman2122/p-ui/internal/node/admission"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

type (
	Lookup    func(client string) (*limits.Account, bool)
	lookupKey struct{}
)

func WithAccounts(ctx context.Context, lookup Lookup) context.Context {
	return context.WithValue(ctx, lookupKey{}, lookup)
}

func Install(config *core.Config) error {
	if singbridge.PenhoonPatchLevel != 7 || shadowaead_2022.PenhoonAuthenticationPatchLevel != 1 {
		return errors.New("unsupported Xray node patch level")
	}
	for i, app := range config.App {
		if app.Type == serial.ToTypedMessage(&dispatcher.Config{}).Type {
			config.App[i] = serial.ToTypedMessage(&Config{})
			return nil
		}
	}
	return errors.New("xray configuration has no default dispatcher")
}

func init() {
	common.Must(common.RegisterConfig((*Config)(nil), func(ctx context.Context, _ interface{}) (interface{}, error) {
		lookup, ok := ctx.Value(lookupKey{}).(Lookup)
		if !ok || lookup == nil {
			return nil, errors.New("missing client enforcement registry")
		}
		inner, err := common.CreateObject(ctx, &dispatcher.Config{})
		if err != nil {
			return nil, err
		}
		base, ok := inner.(routing.Dispatcher)
		if !ok {
			return nil, fmt.Errorf("unexpected dispatcher %T", inner)
		}
		identities, _ := ctx.Value(identityKey{}).(map[string]map[string]string)
		authentication, _ := ctx.Value(authenticationKey{}).(*Authentication)
		return &guardedDispatcher{Dispatcher: base, lookup: lookup, identities: identities, admission: admission.FromContext(ctx), authentication: authentication}, nil
	}))
}

type guardedDispatcher struct {
	routing.Dispatcher
	lookup         Lookup
	identities     map[string]map[string]string
	admission      admission.Handler
	authentication *Authentication
}

func (d *guardedDispatcher) account(ctx context.Context) (*limits.Account, string, *authentication, error) {
	inbound := session.InboundFromContext(ctx)
	if inbound == nil || inbound.User == nil || inbound.User.Email == "" {
		return nil, "", nil, limits.ErrBlocked
	}
	client := inbound.User.Email
	resolved, _ := ctx.Value(normalizedIdentityKey{}).(normalizedIdentity)
	var authenticated *authentication
	if d.authentication != nil {
		if resolved.dispatcher == d {
			authenticated = resolved.authentication
		} else {
			authenticated = d.authentication.resolve(inbound.User, inbound.Tag)
		}
		if !authenticated.current() {
			return nil, "", nil, limits.ErrBlocked
		}
		client = authenticated.subject.ClientID
	} else if bindings, mapped := d.identities[inbound.Tag]; mapped && (resolved.dispatcher != d || resolved.client != client) {
		client = bindings[client]
		if client == "" {
			return nil, "", nil, limits.ErrBlocked
		}
	}
	account, ok := d.lookup(client)
	if !ok || account == nil {
		d.request(ctx, authenticated, resolved, client, inbound.Tag)
		return nil, "", nil, limits.ErrBlocked
	}
	if err := account.Check(); err != nil {
		d.request(ctx, authenticated, resolved, client, inbound.Tag)
		return nil, "", nil, err
	}
	// Splice bypasses link wrappers and therefore cannot enforce a byte grant.
	inbound.CanSpliceCopy = 3
	return account, client, authenticated, nil
}

func (d *guardedDispatcher) request(ctx context.Context, authenticated *authentication, resolved normalizedIdentity, client, tag string) {
	if resolved.dispatcher == d {
		return
	}
	if authenticated != nil && authenticated.scoped {
		_ = authenticated.proof.Request(ctx)
	} else if d.admission != nil {
		_ = d.admission(ctx, client, tag)
	}
}

func (d *guardedDispatcher) Dispatch(ctx context.Context, dest net.Destination) (*transport.Link, error) {
	account, client, authenticated, err := d.account(ctx)
	if err != nil {
		return nil, err
	}
	link, err := d.Dispatcher.Dispatch(d.identityContext(ctx, client, authenticated), dest)
	if err != nil {
		return nil, err
	}
	packet := dest.Network == net.Network_UDP
	ctx = authenticated.guard(ctx, link)
	return &transport.Link{
		Reader: newReader(ctx, link.Reader, account, limits.Download, packet),
		Writer: &writer{ctx: ctx, base: link.Writer, account: account, dir: limits.Upload, packet: packet},
	}, nil
}

func (d *guardedDispatcher) DispatchLink(ctx context.Context, dest net.Destination, link *transport.Link) error {
	account, client, authenticated, err := d.account(ctx)
	if err != nil {
		return err
	}
	packet := dest.Network == net.Network_UDP
	ctx = authenticated.guard(ctx, link)
	return d.Dispatcher.DispatchLink(d.identityContext(ctx, client, authenticated), dest, &transport.Link{
		Reader: newReader(ctx, link.Reader, account, limits.Upload, packet),
		Writer: &writer{ctx: ctx, base: link.Writer, account: account, dir: limits.Download, packet: packet},
	})
}
