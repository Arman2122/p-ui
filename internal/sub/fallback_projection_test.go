package sub

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
The legacy unix-socket fallback projection ran SQLite SQL on PostgreSQL.

A fallback child listening on an `@abstract-socket` has no address a client can
reach, so before it is rendered into a link it is projected through its master —
the master's listen address, port and stream settings. Two ways to find that
master: a row in `inbound_fallbacks`, or, for a config written before that table
existed, by searching every inbound's `settings.fallbacks` array for one whose
`dest` is this socket.

The second used `JSON_TYPE(settings, '$.fallbacks')`, the two-argument
`json_each(settings, '$.fallbacks')` and `json_extract(value, '$.dest')` — all
SQLite. This codebase is PostgreSQL-only, so the query errored on every call.
Nothing noticed, because the error was compared to nil in an `if err == nil`
and dropped on the floor: a legacy child simply never found its master and
handed out `@socket` as its address. F127.

Rewritten with the jsonb form `internal/database/dialect.go` already uses, and
the error is now logged unless it is the ordinary not-found.
*/
func TestALegacyFallbackChildFindsItsMasterThroughTheSettingsBlob(t *testing.T) {
	initSubDB(t)
	db := database.GetDB()

	// The master carries its fallback in settings only — no inbound_fallbacks
	// row — which is the pre-table shape this path exists for.
	master := &model.Inbound{
		UserId: 1, Tag: "legacy-master", Enable: true, Listen: "203.0.113.40", Port: 8443,
		Protocol: model.VLESS,
		Settings: `{"clients":[],"decryption":"none","fallbacks":[{"dest":"@legacy-child-socket"}]}`,
		StreamSettings: `{"network":"tcp","security":"tls",` +
			`"tlsSettings":{"serverName":"legacy.example","settings":{"fingerprint":"chrome"}}}`,
	}
	if err := db.Create(master).Error; err != nil {
		t.Fatalf("seed master: %v", err)
	}

	child := &model.Inbound{
		UserId: 1, Tag: "legacy-child", Enable: true, Listen: "@legacy-child-socket", Port: 0,
		Protocol: model.VLESS,
		Settings: `{"clients":[{"id":"11111111-2222-4333-8444-555555555555","email":"legacy@e"}],"decryption":"none"}`,
		StreamSettings: `{"network":"ws","wsSettings":{"path":"/legacy"}}`,
	}
	if err := db.Create(child).Error; err != nil {
		t.Fatalf("seed child: %v", err)
	}

	projected := *child
	if !(&SubService{}).projectThroughFallbackMaster(&projected) {
		t.Fatal("the child did not find its master, so its link would hand out the abstract socket as an address")
	}
	if projected.Listen != "203.0.113.40" {
		t.Fatalf("listen = %q, want the master's address", projected.Listen)
	}
	if projected.Port != 8443 {
		t.Fatalf("port = %d, want the master's 8443", projected.Port)
	}
}

// A child whose socket no master names is left alone — the ordinary not-found,
// which must stay quiet rather than become a logged failure.
func TestAnUnclaimedSocketChildIsNotProjected(t *testing.T) {
	initSubDB(t)
	db := database.GetDB()

	orphan := &model.Inbound{
		UserId: 1, Tag: "orphan-child", Enable: true, Listen: "@nobody-claims-this", Port: 0,
		Protocol: model.VLESS,
		Settings: `{"clients":[],"decryption":"none"}`,
	}
	if err := db.Create(orphan).Error; err != nil {
		t.Fatalf("seed orphan: %v", err)
	}

	projected := *orphan
	if (&SubService{}).projectThroughFallbackMaster(&projected) {
		t.Fatalf("an unclaimed socket child was projected through something: %+v", projected)
	}
}
