package service

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestPortConflictHonorsIPv6OnlyCoreBinding(t *testing.T) {
	for _, tc := range []struct {
		name, existingListen, incomingListen, stream string
		protocol                                     model.Protocol
		conflict                                     bool
	}{
		{"IPv6 only admits IPv4", "::", "127.0.0.1", `{"network":"tcp","sockopt":{"V6Only":true}}`, model.VLESS, false},
		{"legacy lowercase key", "::", "127.0.0.1", `{"network":"tcp","sockopt":{"v6only":true}}`, model.VLESS, false},
		{"dual stack keeps IPv4 reserved", "::", "127.0.0.1", `{"network":"tcp"}`, model.VLESS, true},
		{"expanded wildcard keeps IPv4 reserved", "0:0:0:0:0:0:0:0", "127.0.0.1", `{"network":"tcp"}`, model.VLESS, true},
		{"IPv4 wildcard is dual stack", "0.0.0.0", "::1", `{"network":"tcp"}`, model.VLESS, true},
		{"IPv6 wildcard still owns IPv6", "::", "::1", `{"network":"tcp","sockopt":{"V6Only":true}}`, model.VLESS, true},
		{"native core ignores Xray socket options", "::", "127.0.0.1", `{"sockopt":{"V6Only":true}}`, model.MTProto, true},
		{"same IPv6 in different notation", "2001:db8::1", "2001:0db8:0:0:0:0:0:1", `{"network":"tcp"}`, model.VLESS, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setupConflictDB(t)
			seedInboundConflict(t, "family-existing", tc.existingListen, 23461, tc.protocol, tc.stream, `{"clients":[]}`)
			hit, err := (&InboundService{}).checkPortConflict(&model.Inbound{Listen: tc.incomingListen, Port: 23461, Protocol: model.VLESS, StreamSettings: `{"network":"tcp"}`}, 0)
			if err != nil {
				t.Fatal(err)
			}
			if tc.conflict {
				if hit == nil || hit.Tag != "family-existing" {
					t.Fatalf("conflict=%v, want family-existing", hit)
				}
			} else if hit != nil {
				t.Fatalf("separate address family rejected: %s", hit)
			}
		})
	}
}
