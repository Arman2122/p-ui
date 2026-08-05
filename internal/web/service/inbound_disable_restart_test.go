package service

import (
	"errors"
	"testing"
)

/*
The handler-not-found case is the one that bit in production: an mtproto inbound
is served by an mtg sidecar and is absent from Xray's config, so every depleted
MTProto client made the quota job ask for an Xray restart — dropping every live
Xray connection on the box, while doing nothing to the secret it meant to revoke.

The strings are verbatim from a running core, so a reworded upstream error fails
here rather than silently restoring the restart.
*/
func TestRestartCannotFix(t *testing.T) {
	const email = "quota@test"
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "tag belongs to another core",
			err:  errors.New("failed to remove user: rpc error: code = Unknown desc = app/proxyman/command: failed to get handler: live-mt-30444 > app/proxyman/inbound: handler not found: live-mt-30444"),
			want: true,
		},
		{
			name: "user is already gone",
			err:  errors.New("failed to remove user: rpc error: code = Unknown desc = User quota@test not found."),
			want: true,
		},
		{
			name: "a reachable xray that genuinely failed still earns a restart",
			err:  errors.New("failed to remove user: rpc error: code = Unavailable desc = connection refused"),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := restartCannotFix(tt.err, email); got != tt.want {
				t.Fatalf("restartCannotFix(%q) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
