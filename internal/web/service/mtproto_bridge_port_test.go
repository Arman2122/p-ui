package service

import (
	"encoding/json"
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
The mtproto bridge never lands on a port the panel already claims.

Three ways a port got here and none was checked. The stored one carries across
edits, so an inbound created on that port later takes it. FreeLocalPort binds
127.0.0.1:0 and closes it, which says the OS had it free for an instant and
nothing about what the panel is about to generate. And an API caller can send
any number at all.

injectMtprotoEgress DOES check, and skips the bridge on a collision -- so the
inbound saved clean, displayed as routed, and carried no Telegram traffic, with
one log line on the panel to say why. F035.
*/
func bridgePortOf(t *testing.T, ib *model.Inbound) int {
	t.Helper()
	var parsed map[string]any
	if err := json.Unmarshal([]byte(ib.Settings), &parsed); err != nil {
		t.Fatalf("settings: %v", err)
	}
	return settingsRouteXrayPort(parsed)
}

func routedMtproto(tag string, port int, bridge int) *model.Inbound {
	settings := `{"routeThroughXray":true,"clients":[]}`
	if bridge > 0 {
		settings = `{"routeThroughXray":true,"routeXrayPort":` + itoa(bridge) + `,"clients":[]}`
	}
	return &model.Inbound{
		Tag: tag, Enable: true, Port: port, Protocol: model.MTProto, Settings: settings,
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// A caller-supplied port that a real inbound already binds is replaced, not
// stored: the bridge would otherwise be skipped at generation time.
func TestACallerSuppliedBridgePortThatIsTakenIsReplaced(t *testing.T) {
	setupConflictDB(t)
	seedInboundConflict(t, "vless-holder", "127.0.0.1", 47501, model.VLESS, "",
		`{"clients":[],"decryption":"none"}`)

	created, _, _, err := (&InboundService{}).AddInbound(routedMtproto("mt-collide", 47502, 47501))
	if err != nil {
		t.Fatalf("AddInbound: %v", err)
	}
	if got := bridgePortOf(t, created); got == 47501 {
		t.Fatalf("the bridge was stored on port %d, which vless-holder binds on loopback; injectMtprotoEgress skips the bridge and this inbound carries no traffic", got)
	}
}

// And the stored port is re-checked on every save, because the collision can
// arrive after the mtproto inbound was created.
func TestAStoredBridgePortThatBecameTakenIsReallocated(t *testing.T) {
	setupConflictDB(t)

	created, _, _, err := (&InboundService{}).AddInbound(routedMtproto("mt-first", 47503, 0))
	if err != nil {
		t.Fatalf("AddInbound: %v", err)
	}
	bridge := bridgePortOf(t, created)
	if bridge <= 0 {
		t.Fatal("no bridge port was allocated at all")
	}

	// Something else takes that port afterwards.
	seedInboundConflict(t, "vless-later", "127.0.0.1", bridge, model.VLESS, "",
		`{"clients":[],"decryption":"none"}`)

	edit := *created
	edit.Remark = "edited"
	saved, _, _, err := (&InboundService{}).UpdateInbound(&edit)
	if err != nil {
		t.Fatalf("UpdateInbound: %v", err)
	}
	if got := bridgePortOf(t, saved); got == bridge {
		t.Fatalf("the bridge kept port %d after another inbound took it; its Telegram traffic silently stops", got)
	}
}

// A port nothing claims is left exactly as it was: the bridge must not move
// under a working inbound on every save.
func TestAFreeBridgePortIsKeptAcrossEdits(t *testing.T) {
	setupConflictDB(t)

	created, _, _, err := (&InboundService{}).AddInbound(routedMtproto("mt-stable", 47504, 0))
	if err != nil {
		t.Fatalf("AddInbound: %v", err)
	}
	bridge := bridgePortOf(t, created)

	edit := *created
	edit.Remark = "edited"
	saved, _, _, err := (&InboundService{}).UpdateInbound(&edit)
	if err != nil {
		t.Fatalf("UpdateInbound: %v", err)
	}
	if got := bridgePortOf(t, saved); got != bridge {
		t.Fatalf("the bridge moved from %d to %d on an unrelated edit; every live Telegram connection through it drops", bridge, got)
	}
}
