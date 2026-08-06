package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

// stubCore is a core with no capabilities: coreViews reads only the descriptor
// and the kinds, so binding real ones would test Bind rather than the view.
type stubCore struct {
	descriptor core.Descriptor
	kinds      []core.Kind
}

func (s stubCore) Describe() core.Descriptor         { return s.descriptor }
func (s stubCore) Kinds() []core.Kind                { return s.kinds }
func (s stubCore) Preflight(_ context.Context) error { return nil }

// declaringCore answers per kind, the shape a real multi-kind core has: one
// Describe, one set of credentials per protocol it serves.
type declaringCore struct {
	stubCore
	credentials map[core.Kind][]string
}

func (d declaringCore) ClientCredentials(kind core.Kind) []string { return d.credentials[kind] }

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
				`"clientCredentials":{}},` +
				`{"id":"xray","titleKey":"cores.xray.title","kinds":["shadowsocks","trojan","vless"],` +
				`"caps":{"userHotAdd":true,"perUserStats":true,"quotaPushdown":false,"onlineUsers":true,"shareLink":false},` +
				`"clientCredentials":{}}]`,
		},
		{
			name: "an unanswered capability stays null, never false",
			reg:  registryOf(t, unanswered),
			want: `[{"id":"future","titleKey":"cores.future.title","kinds":["future"],` +
				`"caps":{"userHotAdd":null,"perUserStats":null,"quotaPushdown":null,"onlineUsers":null,"shareLink":null},` +
				`"clientCredentials":{}}]`,
		},
		{
			name: "credentials are keyed per kind, and a kind the core skips is absent, not empty",
			reg:  registryOf(t, declaring),
			want: `[{"id":"multi","titleKey":"cores.multi.title","kinds":["tun","vless","vmess"],` +
				`"caps":{"userHotAdd":null,"perUserStats":null,"quotaPushdown":null,"onlineUsers":null,"shareLink":null},` +
				`"clientCredentials":{"vless":["uuid"],"vmess":["uuid","security"]}}]`,
		},
		{
			name: "no registry is an empty list, not null",
			reg:  nil,
			want: `[]`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(coreViews(tc.reg))
			if err != nil {
				t.Fatalf("marshal views: %v", err)
			}
			if got := string(encoded); got != tc.want {
				t.Errorf("coreViews() = %s, want %s", got, tc.want)
			}
		})
	}
}
