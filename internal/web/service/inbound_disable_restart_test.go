package service

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
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
			/*
				A depleted client on an http/mixed/tunnel/tun inbound. Xray serves
				the tag, so "handler not found" never fires, but none of those four
				proxies is a UserManager -- restarting cannot make one. F314.
			*/
			name: "the kind Xray serves holds no users",
			err:  errors.New("failed to remove user: rpc error: code = Unknown desc = app/proxyman/command: proxy is not a UserManager"),
			want: true,
		},
		{
			/*
				The contract's own answer, decided before any call is made. The
				string arms stay for what still arrives as text -- a remote node,
				or a panel running an older build. F314.
			*/
			name: "the kind is declared to hold no users",
			err:  fmt.Errorf("%w: %s", core.ErrKindHasNoUsers, "tun"),
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

/*
A depleted inbound whose removal failed must schedule a restart.

An inbound that runs out of quota is dropped from the core by tag and flipped to
enable=false. If that drop fails -- and on a box with no running core every drop
fails -- the listener is still up, and nothing rebuilds the config without it:
the count of disabled INBOUNDS never fed the restart signal, only the count of
disabled CLIENTS did. So a depleted inbound kept serving every session on it,
indefinitely, while the panel showed it as off.

Scoped to the inbounds XRAY serves, deliberately. Feeding the raw count in would
force a full local Xray restart -- dropping every live Xray session on the box --
for a depleted MTProto or kernel-WireGuard inbound, which buys nothing, because
those are not in the Xray config at all.

The count is taken off the doomed set BEFORE the drop is attempted and
independently of its outcome. That is load-bearing: the database flip happens
either way, and if a later edit counted only successful drops the fix would stop
working on exactly the headless box where every drop fails. F338.
*/
func TestADepletedXrayInboundSchedulesARestart(t *testing.T) {
	setupConflictDB(t)
	db := database.GetDB()

	seedInboundConflict(t, "vless-depleted", "", 46181, model.VLESS, "",
		`{"clients":[],"decryption":"none"}`)
	ib := loadInboundByTag(t, "vless-depleted")
	if err := db.Model(&model.Inbound{}).Where("id = ?", ib.Id).
		Updates(map[string]any{"total": 100, "up": 60, "down": 60}).Error; err != nil {
		t.Fatalf("deplete the inbound: %v", err)
	}

	_, disabled, xrayServed, err := (&InboundService{}).disableInvalidInbounds(db)
	if err != nil {
		t.Fatalf("disableInvalidInbounds: %v", err)
	}
	if disabled != 1 {
		t.Fatalf("the inbound was not disabled at all: disabled=%d", disabled)
	}
	if xrayServed != 1 {
		t.Fatalf("a depleted xray-served inbound was not counted toward the restart signal (got %d); its listener stays up serving every session already on it", xrayServed)
	}
}

// The other half: a core Xray does not serve must NOT force an Xray restart.
func TestADepletedNonXrayInboundDoesNotScheduleAnXrayRestart(t *testing.T) {
	setupConflictDB(t)
	db := database.GetDB()

	seedInboundConflict(t, "mt-depleted", "", 46182, model.MTProto, "",
		`{"clients":[],"routeThroughXray":false}`)
	ib := loadInboundByTag(t, "mt-depleted")
	if err := db.Model(&model.Inbound{}).Where("id = ?", ib.Id).
		Updates(map[string]any{"total": 100, "up": 60, "down": 60}).Error; err != nil {
		t.Fatalf("deplete the inbound: %v", err)
	}

	_, disabled, xrayServed, err := (&InboundService{}).disableInvalidInbounds(db)
	if err != nil {
		t.Fatalf("disableInvalidInbounds: %v", err)
	}
	if disabled != 1 {
		t.Fatalf("the inbound was not disabled: disabled=%d", disabled)
	}
	if xrayServed != 0 {
		t.Errorf("a depleted MTProto inbound counted toward the Xray restart signal (got %d); that would drop every live Xray session to tear down something Xray never served", xrayServed)
	}
}
