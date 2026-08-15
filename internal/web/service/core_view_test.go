package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

// stubCore is a core with no capabilities: coreViews reads only the descriptor,
// the kinds and Preflight, so binding real ones would test Bind rather than the
// view. unusable is the Preflight error a host that cannot run this core gets.
type stubCore struct {
	descriptor core.Descriptor
	kinds      []core.Kind
	unusable   error
}

func (s stubCore) Describe() core.Descriptor         { return s.descriptor }
func (s stubCore) Kinds() []core.Kind                { return s.kinds }
func (s stubCore) Preflight(_ context.Context) error { return s.unusable }

// declaringCore answers per kind, the shape a real multi-kind core has: one
// Describe, one set of credentials per protocol it serves.
type declaringCore struct {
	stubCore
	credentials map[core.Kind][]string
}

func (d declaringCore) ClientCredentials(kind core.Kind) []string { return d.credentials[kind] }

func (d declaringCore) MintClientCredentials(core.Kind, string, map[string]string) (map[string]string, error) {
	return map[string]string{}, nil
}

func (d declaringCore) ValidateClient(core.Kind, string, string, map[string]string) error { return nil }

func (d declaringCore) ClientIdentity(core.Kind) string { return "email" }

// exitingCore terminates a route on some of its kinds and not others, which is
// the whole distinction between the outbound half of Kinds and the inbound half.
type exitingCore struct {
	stubCore
	exits map[core.Kind]core.ExitHandleKind
}

func (e exitingCore) ExitKinds() []core.Kind {
	out := make([]core.Kind, 0, len(e.exits))
	for kind := range e.exits {
		out = append(out, kind)
	}
	return out
}

func (e exitingCore) ExitHandleKind(kind core.Kind) core.ExitHandleKind { return e.exits[kind] }

func (e exitingCore) ExitHandle(context.Context, core.Exit) (core.ExitHandle, error) {
	return core.ExitHandle{}, nil
}

func registryOf(t *testing.T, cores ...core.Core) *core.Registry {
	t.Helper()
	reg := core.NewRegistry()
	for _, c := range cores {
		if err := reg.Register(c); err != nil {
			t.Fatalf("register %q: %v", c.Describe().ID, err)
		}
	}
	return reg
}

func TestCoreViews(t *testing.T) {
	xray := stubCore{
		descriptor: core.Descriptor{
			ID:       "xray",
			TitleKey: "cores.xray.title",
			Caps: core.Capabilities{
				UserHotAdd:    core.Yes(),
				PerUserStats:  core.Yes(),
				QuotaPushdown: core.No(),
				OnlineUsers:   core.Yes(),
				ShareLink:     core.No(),
			},
		},
		kinds: []core.Kind{"vless", "trojan", "shadowsocks"},
	}
	mtproto := stubCore{
		descriptor: core.Descriptor{
			ID:       "mtproto",
			TitleKey: "cores.mtproto.title",
			Caps: core.Capabilities{
				UserHotAdd:    core.Yes(),
				PerUserStats:  core.Yes(),
				QuotaPushdown: core.Yes(),
				OnlineUsers:   core.Yes(),
				ShareLink:     core.No(),
			},
		},
		kinds: []core.Kind{"mtproto"},
	}
	unanswered := stubCore{
		descriptor: core.Descriptor{ID: "future", TitleKey: "cores.future.title"},
		kinds:      []core.Kind{"future"},
	}
	declaring := declaringCore{
		stubCore: stubCore{
			descriptor: core.Descriptor{ID: "multi", TitleKey: "cores.multi.title"},
			kinds:      []core.Kind{"vmess", "vless", "tun"},
		},
		credentials: map[core.Kind][]string{
			"vmess": {core.CredUUID, core.CredSecurity},
			"vless": {core.CredUUID},
		},
	}
	unusable := stubCore{
		descriptor: core.Descriptor{ID: "wgkernel", TitleKey: "cores.wgkernel.title"},
		kinds:      []core.Kind{"wgkernel"},
		unusable:   errors.New("wireguard: no kernel support on this host"),
	}
	exiting := exitingCore{
		stubCore: stubCore{
			descriptor: core.Descriptor{ID: "wireguard", TitleKey: "cores.wireguard.title"},
			kinds:      []core.Kind{"wgkernel", "wgtunnel"},
		},
		exits: map[core.Kind]core.ExitHandleKind{
			"wgkernel": core.ExitDevice,
			"wgtunnel": core.ExitNone,
		},
	}

	tests := []struct {
		name string
		reg  *core.Registry
		want string
	}{
		{
			name: "cores and their kinds are sorted, not left in declaration order",
			reg:  registryOf(t, xray, mtproto),
			want: `[{"id":"mtproto","titleKey":"cores.mtproto.title","kinds":["mtproto"],` +
				`"caps":{"userHotAdd":true,"perUserStats":true,"quotaPushdown":true,"onlineUsers":true,"shareLink":false},` +
				`"exitKinds":[],"clientCredentials":{},"shaping":{},"available":true,"unavailable":""},` +
				`{"id":"xray","titleKey":"cores.xray.title","kinds":["shadowsocks","trojan","vless"],` +
				`"caps":{"userHotAdd":true,"perUserStats":true,"quotaPushdown":false,"onlineUsers":true,"shareLink":false},` +
				`"exitKinds":[],"clientCredentials":{},"shaping":{},"available":true,"unavailable":""}]`,
		},
		{
			name: "an unanswered capability stays null, never false",
			reg:  registryOf(t, unanswered),
			want: `[{"id":"future","titleKey":"cores.future.title","kinds":["future"],` +
				`"caps":{"userHotAdd":null,"perUserStats":null,"quotaPushdown":null,"onlineUsers":null,"shareLink":null},` +
				`"exitKinds":[],"clientCredentials":{},"shaping":{},"available":true,"unavailable":""}]`,
		},
		{
			name: "credentials are keyed per kind, and a kind the core skips is absent, not empty",
			reg:  registryOf(t, declaring),
			want: `[{"id":"multi","titleKey":"cores.multi.title","kinds":["tun","vless","vmess"],` +
				`"caps":{"userHotAdd":null,"perUserStats":null,"quotaPushdown":null,"onlineUsers":null,"shareLink":null},` +
				`"exitKinds":[],"clientCredentials":{"vless":["uuid"],"vmess":["uuid","security"]},"shaping":{},"available":true,"unavailable":""}]`,
		},
		{
			name: "a core this host cannot run says so, and says why, instead of being offered",
			reg:  registryOf(t, unusable),
			want: `[{"id":"wgkernel","titleKey":"cores.wgkernel.title","kinds":["wgkernel"],` +
				`"caps":{"userHotAdd":null,"perUserStats":null,"quotaPushdown":null,"onlineUsers":null,"shareLink":null},` +
				`"exitKinds":[],"clientCredentials":{},"shaping":{},"available":false,"unavailable":"wireguard: no kernel support on this host"}]`,
		},
		{
			name: "only a kind that can terminate a route is offered as an exit",
			reg:  registryOf(t, exiting),
			want: `[{"id":"wireguard","titleKey":"cores.wireguard.title","kinds":["wgkernel","wgtunnel"],` +
				`"caps":{"userHotAdd":null,"perUserStats":null,"quotaPushdown":null,"onlineUsers":null,"shareLink":null},` +
				`"exitKinds":["wgkernel"],"clientCredentials":{},"shaping":{},"available":true,"unavailable":""}]`,
		},
		{
			name: "no registry is an empty list, not null",
			reg:  nil,
			want: `[]`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(coreViews(t.Context(), tc.reg))
			if err != nil {
				t.Fatalf("marshal views: %v", err)
			}
			if got := string(encoded); got != tc.want {
				t.Errorf("coreViews() = %s, want %s", got, tc.want)
			}
		})
	}
}
