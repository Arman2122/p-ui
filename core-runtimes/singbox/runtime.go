package singbox

import (
	"context"

	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/adapter/certificate"
	"github.com/sagernet/sing-box/adapter/endpoint"
	"github.com/sagernet/sing-box/adapter/inbound"
	"github.com/sagernet/sing-box/adapter/outbound"
	"github.com/sagernet/sing-box/adapter/service"
	"github.com/sagernet/sing-box/dns"
	"github.com/sagernet/sing-box/dns/transport/local"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing-box/protocol/anytls"
	"github.com/sagernet/sing-box/protocol/direct"
	"github.com/sagernet/sing-box/protocol/tuic"

	"github.com/Arman2122/p-ui/internal/node/admission"
)

func Context(ctx context.Context) context.Context {
	in := inbound.NewRegistry()
	anytls.RegisterInbound(in)
	tuic.RegisterInbound(in)
	out := outbound.NewRegistry()
	anytls.RegisterOutbound(out)
	tuic.RegisterOutbound(out)
	direct.RegisterOutbound(out)
	resolvers := dns.NewTransportRegistry()
	local.RegisterTransport(resolvers)
	return box.Context(ctx, in, out, endpoint.NewRegistry(), resolvers, service.NewRegistry(), certificate.NewRegistry())
}

func New(ctx context.Context, options option.Options, lookup Lookup) (*box.Box, error) {
	instance, err := box.New(box.Options{Options: options, Context: Context(ctx)})
	if err != nil {
		return nil, err
	}
	authentication, _ := ctx.Value(authenticationKey{}).(*authenticationRegistry)
	instance.Router().AppendTracker(&Tracker{Lookup: lookup, Demand: admission.FromContext(ctx), authentication: authentication})
	return instance, nil
}
