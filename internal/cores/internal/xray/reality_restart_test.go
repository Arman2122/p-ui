package xray

import (
	"slices"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

/*
A REALITY parameter change must never be hot-applied, and F227 is why that now
matters.

Before f1c7071d an inbound edit was pushed as DelInbound+AddInbound, two
one-sided diffs in which the tag exists on only one side — so the guard added by
892c06c8, which requires the tag on both, never fired. F234 read that correctly.
What it inferred from it does not hold: measured against the pinned core, a
gRPC remove+add DOES rebuild the authenticator (RUNTIME-RESULTS §9), so the old
key stopped working and the new one started, three rotations running.

f1c7071d then replaced the pair with one converge-in-place call, which is what
makes the guard load-bearing rather than dead: PlanChange and ApplyInstance both
build a same-tag diff, the guard fires, and the edit is escalated to a restart.
Nothing else pins that link, and losing it would put REALITY edits back on the
hot path with no signal.
*/
func TestAREALITYKeyRotationIsNeverHotApplied(t *testing.T) {
	c := New(Deps{})
	const clients = `{"clients":[{"email":"a@example.com","id":"aaa"}],"decryption":"none"}`

	base := core.Instance{
		ID: 1, Kind: "vless", Tag: "in-1", Listen: "127.0.0.1", Port: 443, Enable: true,
		Settings: clients,
		StreamSettings: `{"network":"tcp","security":"reality","realitySettings":` +
			`{"dest":"www.cloudflare.com:443","serverNames":["www.cloudflare.com"],` +
			`"privateKey":"4P99uJ4QOL8IfKT5DP_ST7dFYpd9EiyAz1vLej5HqHY","shortIds":["01ab"]}}`,
		Users: []core.User{
			{Email: "a@example.com", Enable: true, Credentials: map[string]any{"id": "aaa"}},
		},
	}

	rotated := base
	rotated.StreamSettings = `{"network":"tcp","security":"reality","realitySettings":` +
		`{"dest":"www.cloudflare.com:443","serverNames":["www.cloudflare.com"],` +
		`"privateKey":"GJ-32SbCXYzXJ_NRkO-EOlm5JpY2U2NR7hXrvqOUyUk","shortIds":["01ab"]}}`

	shortIDRotated := base
	shortIDRotated.StreamSettings = `{"network":"tcp","security":"reality","realitySettings":` +
		`{"dest":"www.cloudflare.com:443","serverNames":["www.cloudflare.com"],` +
		`"privateKey":"4P99uJ4QOL8IfKT5DP_ST7dFYpd9EiyAz1vLej5HqHY","shortIds":["beef"]}}`

	// A client added to a REALITY inbound is still a user diff: diffInboundUsers
	// is consulted first, so the common edit does not drag everyone's session
	// through a restart.
	clientAdded := base
	clientAdded.Settings = `{"clients":[{"email":"a@example.com","id":"aaa"},{"email":"b@example.com","id":"bbb"}],"decryption":"none"}`
	clientAdded.Users = append(slices.Clone(base.Users), core.User{
		Email: "b@example.com", Enable: true, Credentials: map[string]any{"id": "bbb"},
	})

	for _, tc := range []struct {
		name string
		next core.Instance
		want core.Action
	}{
		{"privateKey rotated", rotated, core.ActionRestart},
		{"shortId rotated", shortIDRotated, core.ActionRestart},
		{"client added to a REALITY inbound", clientAdded, core.ActionHotApply},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.PlanChange(base, tc.next); got != tc.want {
				t.Fatalf("PlanChange = %s, want %s", got, tc.want)
			}
		})
	}
}
