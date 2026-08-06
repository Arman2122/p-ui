package arch

import (
	"encoding/json"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/runtime"
	"github.com/Arman2122/p-ui/internal/web/service"
)

/*
An inbound whose kind no registered core serves is quarantined: never started,
never deleted, never re-marshalled (docs/multi-core-architecture.md §8).

inbounds.protocol is a plain varchar with no CHECK constraint, so a row naming a
kind this binary has never heard of is already reachable — a downgrade, a node
one release ahead, a core compiled out. The panel must leave it exactly as it
found it.

Behavioural rather than a source grep on purpose: the thing that broke was
RenderInbound skipping non-Xray inbounds by naming mtproto, so an ocserv inbound
would have been rendered into Xray's client shape and appended to the config.
xray-core rejects a protocol it does not know and refuses to start, which takes
every VLESS, VMess and Trojan inbound on the box down with it.
*/

// quarantinedKind stands in for a row a newer panel wrote. It must stay a kind
// no core in this build claims, which the guard asserts before anything else.
const quarantinedKind model.Protocol = "ocserv"

/*
renderQuarantined reports a panic as the failure it is instead of letting it
escape, because an unquarantined inbound reaches the render path's database
reads and there is no database here.

Reporting matters more than usual in this package: a panic aborts the whole test
binary, so every other guard's verdict would be lost along with the message.
*/
func renderQuarantined(t *testing.T, svc *service.XrayService, row *model.Inbound) (*core.InboundConfig, error) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RenderInbound(%q) walked into the Xray render path and panicked (%v) — an inbound no core serves must be turned away before anything reads it", row.Protocol, r)
		}
	}()
	return svc.RenderInbound(row)
}

func TestUnknownCoreRoundTripsByteForByte(t *testing.T) {
	for _, kind := range cores.Kinds() {
		if model.Protocol(kind) == quarantinedKind {
			t.Fatalf("%q is a registered kind now, so this guard certifies nothing — pick a kind no core serves", quarantinedKind)
		}
	}

	row := &model.Inbound{
		Id:             9001,
		Enable:         true,
		Protocol:       quarantinedKind,
		Tag:            "quarantined-in",
		Listen:         "127.0.0.1",
		Port:           4443,
		Settings:       `{"auth":"plain","clients":[{"email":"a@b.c","password":"pw"}],"unknownKey":[1,2]}`,
		StreamSettings: `{"security":"none"}`,
		Sniffing:       `{"enabled":false}`,
	}
	before, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("marshal the fixture row: %v", err)
	}
	svc := &service.XrayService{}

	t.Run("is not rendered into the xray config", func(t *testing.T) {
		rendered, err := renderQuarantined(t, svc, row)
		if err != nil {
			t.Fatalf("RenderInbound(%q) = error %v, want a nil config and a nil error", quarantinedKind, err)
		}
		if rendered != nil {
			t.Errorf("RenderInbound(%q) produced an inbound config (tag %q, protocol %q) — xray-core rejects a protocol it does not know and refuses to start, so every inbound it does serve goes down with it",
				quarantinedKind, rendered.Tag, rendered.Protocol)
		}
	})

	t.Run("round trips byte for byte", func(t *testing.T) {
		after, err := json.Marshal(row)
		if err != nil {
			t.Fatalf("re-marshal the row: %v", err)
		}
		if string(after) != string(before) {
			t.Errorf("the render path rewrote a row no core serves\n--- before ---\n%s\n--- after  ---\n%s", before, after)
		}
	})

	/*
		Quarantine is a loud refusal, not a silent no-op: an inbound the panel
		cannot serve must never look applied, and must never be dropped either.
	*/
	t.Run("is refused rather than dropped", func(t *testing.T) {
		registry, err := cores.Default(cores.Deps{})
		if err != nil {
			t.Fatalf("build the core registry: %v", err)
		}
		local := runtime.NewLocal(runtime.LocalDeps{Cores: registry, RenderInbound: svc.RenderInbound})
		const want = `no core serves protocol "ocserv"`

		for _, op := range []struct {
			name string
			run  func() error
		}{
			{"AddInbound", func() error { return local.AddInbound(t.Context(), row) }},
			{"DelInbound", func() error { return local.DelInbound(t.Context(), row) }},
		} {
			t.Run(op.name, func(t *testing.T) {
				err := op.run()
				if err == nil {
					t.Fatalf("%s(%q) reported success; an inbound no core serves must be refused, never silently applied or dropped", op.name, quarantinedKind)
				}
				if err.Error() != want {
					t.Errorf("%s(%q) = %q, want %q", op.name, quarantinedKind, err, want)
				}
			})
		}
	})
}
