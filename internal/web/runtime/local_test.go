package runtime

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
The case a multi-core panel has and a single-core one could not: a stored protocol
this build does not know, from a newer release or a node running ahead.

Reporting success would leave the admin believing an inbound is served that
nothing is listening on, and a delete that silently "succeeds" turns a quarantine
into a deletion.
*/
func TestAnUnservableInboundFailsLoudly(t *testing.T) {
	ib := &model.Inbound{Id: 1, Protocol: "quicvpn", Tag: "in-1", Enable: true}

	for _, tc := range []struct {
		name string
		deps LocalDeps
		want string
	}{
		{
			name: "no core serves the protocol",
			deps: LocalDeps{Cores: core.NewRegistry()},
			want: "quicvpn",
		},
		{
			name: "no registry was wired at all",
			deps: LocalDeps{},
			want: "registry",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l := NewLocal(tc.deps)
			for op, run := range map[string]func() error{
				"add":    func() error { return l.AddInbound(t.Context(), ib) },
				"delete": func() error { return l.DelInbound(t.Context(), ib) },
				"update": func() error { return l.UpdateInbound(t.Context(), ib, ib) },
			} {
				t.Run(op, func(t *testing.T) {
					err := run()
					if err == nil {
						t.Fatal("an inbound this build cannot serve must report an error, not success")
					}
					if !strings.Contains(err.Error(), tc.want) {
						t.Fatalf("error must say why; got %v, want it to mention %q", err, tc.want)
					}
				})
			}
		})
	}
}
