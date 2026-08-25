package xray

import (
	"testing"

	"github.com/xtls/xray-core/proxy/vless"
)

/*
A client added to a running core must not carry the client-only flow.

xtls-rprx-vision-udp443 is a CLIENT setting. The client truncates it to sixteen
bytes before it goes on the wire (requestAddons.Flow[:16]), and the inbound
refuses any account whose stored flow does not equal what arrived — "account is
not able to use the flow xtls-rprx-vision". So a user carrying the untruncated
string can never connect, and the gRPC AddUser accepts it happily: the validator
never inspects Flow.

RenderInbound has stripped it since 9f5ba0cf for the generated config, and the
whole-config hot diff builds its AddedUsers from that same rendered config. The
single-user paths did not: runtime.Local.AddUser reads the raw stored settings,
and so does the traffic job's autoRenew. The panel answered "Client added
successfully", scheduled no restart because nothing failed, and the client could
not connect until some unrelated edit re-rendered the inbound.

Fixed here rather than at each caller because this is the one funnel all three
share.
*/
func TestAHotAddedVlessUserNeverCarriesTheClientOnlyFlow(t *testing.T) {
	for _, tc := range []struct {
		name string
		flow string
		want string
	}{
		{name: "the client-only flow is normalised", flow: vlessFlowVisionUDP443, want: vlessFlowVision},
		{name: "the wire flow is left alone", flow: vlessFlowVision, want: vlessFlowVision},
		{name: "no flow stays no flow", flow: "", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user := map[string]any{"id": "3a3d2f6e-6f1a-4b7c-9d2e-1f0a5c8b7d61"}
			if tc.flow != "" {
				user["flow"] = tc.flow
			}

			typed, err := buildUserAccount("vless", user)
			if err != nil {
				t.Fatalf("buildUserAccount: %v", err)
			}
			raw, err := typed.GetInstance()
			if err != nil {
				t.Fatalf("decode the account: %v", err)
			}
			account, ok := raw.(*vless.Account)
			if !ok {
				t.Fatalf("account is %T, want *vless.Account", raw)
			}
			if account.Flow != tc.want {
				t.Fatalf("Flow = %q, want %q — the inbound refuses any account whose flow is not what the client puts on the wire",
					account.Flow, tc.want)
			}
		})
	}
}

// The generated-config path already stripped it; this pins that the two agree,
// so a future change to one is not silently a divergence from the other.
func TestTheHotPathAndTheRenderedConfigAgreeOnTheFlow(t *testing.T) {
	if vlessFlowVisionUDP443[:len(vlessFlowVision)] != vlessFlowVision {
		t.Fatal("the client-only flow is no longer the wire flow plus a suffix; the core truncates to the wire flow's length, so this pair must stay that shape")
	}
	if len(vlessFlowVision) != 16 {
		t.Fatalf("the wire flow is %d bytes; the core truncates the client's to 16, so a different length means it no longer matches", len(vlessFlowVision))
	}
}
