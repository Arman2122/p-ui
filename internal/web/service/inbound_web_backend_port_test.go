package service

import (
	"encoding/json"
	"testing"

	"github.com/op/go-logging"

	"github.com/Arman2122/p-ui/internal/database/model"
	puilogger "github.com/Arman2122/p-ui/internal/logger"
)

func webBackendPortOf(t *testing.T, settings string) int {
	t.Helper()
	var parsed map[string]any
	if err := json.Unmarshal([]byte(settings), &parsed); err != nil {
		t.Fatalf("settings not valid JSON: %v\n%s", err, settings)
	}
	return settingsPort(parsed, "webBackendPort")
}

// normalizeWebBackendPort logs when it discards a claimed port, and the panel
// logger is a process-wide singleton that starts nil.
func webBackendService(t *testing.T) *InboundService {
	t.Helper()
	portConflictLoggerOnce.Do(func() { puilogger.InitLogger(logging.ERROR) })
	return &InboundService{}
}

// The port is backend-owned, and the renderer emits no [[listener]] for zero, so
// until something allocated one the feature could not be switched on at all.
func TestNormalizeWebBackendPort(t *testing.T) {
	s := webBackendService(t)

	// The gate is the bridge port's: an inbound Xray serves has no sidecar.
	t.Run("an inbound xray serves is untouched", func(t *testing.T) {
		ib := &model.Inbound{Protocol: model.VLESS, Settings: `{"webBackendEnable":true}`}
		if err := s.normalizeWebBackendPort(ib, ""); err != nil {
			t.Fatal(err)
		}
		if ib.Settings != `{"webBackendEnable":true}` {
			t.Fatalf("settings must be untouched, got %s", ib.Settings)
		}
	})

	t.Run("enabling allocates a port", func(t *testing.T) {
		ib := &model.Inbound{Protocol: model.MTProto, Settings: `{"webBackendEnable":true}`}
		if err := s.normalizeWebBackendPort(ib, ""); err != nil {
			t.Fatal(err)
		}
		if p := webBackendPortOf(t, ib.Settings); p <= 0 {
			t.Fatalf("an enabled WEB backend must get a port, got %d", p)
		}
	})

	t.Run("the stored port survives a second save", func(t *testing.T) {
		ib := &model.Inbound{Protocol: model.MTProto, Settings: `{"webBackendEnable":true,"webBackendPort":99999}`}
		if err := s.normalizeWebBackendPort(ib, `{"webBackendEnable":true,"webBackendPort":51100}`); err != nil {
			t.Fatal(err)
		}
		if p := webBackendPortOf(t, ib.Settings); p != 51100 {
			t.Fatalf("the stored port must win over the echoed one, got %d", p)
		}
	})

	t.Run("an unchanged port is not re-marshaled", func(t *testing.T) {
		const stable = `{"webBackendEnable":true,"webBackendPort":51200}`
		ib := &model.Inbound{Protocol: model.MTProto, Settings: stable}
		if err := s.normalizeWebBackendPort(ib, ""); err != nil {
			t.Fatal(err)
		}
		if ib.Settings != stable {
			t.Fatalf("stable settings must pass through untouched, got %s", ib.Settings)
		}
	})

	t.Run("disabling drops the port", func(t *testing.T) {
		ib := &model.Inbound{Protocol: model.MTProto, Settings: `{"webBackendEnable":false,"webBackendPort":51300}`}
		if err := s.normalizeWebBackendPort(ib, ""); err != nil {
			t.Fatal(err)
		}
		if p := webBackendPortOf(t, ib.Settings); p != 0 {
			t.Fatalf("disabling must drop the port, got %d", p)
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(ib.Settings), &parsed); err != nil {
			t.Fatal(err)
		}
		if _, ok := parsed["webBackendPort"]; ok {
			t.Fatalf("the key itself must go, got %s", ib.Settings)
		}
	})
}

// Both ports are loopback binds of the same sidecar. The bridge one is stored in
// settings, so loopbackPortIsClaimed cannot see it and must be checked here.
func TestWebBackendPortRefusesThisInboundsBridgePort(t *testing.T) {
	s := webBackendService(t)
	const bridged = `{"webBackendEnable":true,"routeThroughXray":true,"routeXrayPort":51400,"webBackendPort":51400}`

	cases := map[string]string{
		"echoed by the client": "",
		"carried from storage": `{"webBackendEnable":true,"webBackendPort":51400}`,
	}
	for name, old := range cases {
		t.Run(name, func(t *testing.T) {
			ib := &model.Inbound{Protocol: model.MTProto, Settings: bridged}
			if err := s.normalizeWebBackendPort(ib, old); err != nil {
				t.Fatal(err)
			}
			p := webBackendPortOf(t, ib.Settings)
			if p == 51400 {
				t.Fatal("the WEB backend must not take the Xray bridge port of the same inbound")
			}
			if p <= 0 {
				t.Fatalf("a replacement port must still be allocated, got %d", p)
			}
			if routeXrayPortOf(t, ib.Settings) != 51400 {
				t.Fatalf("the bridge port must be left alone, got %s", ib.Settings)
			}
		})
	}
}

// A port a real inbound already binds on loopback: mtg's extra-listener bind
// would lose silently, so the save must land somewhere else.
func TestWebBackendPortRefusesAPortAnotherInboundBinds(t *testing.T) {
	setupConflictDB(t)
	seedInboundConflict(t, "vless-holder", "127.0.0.1", 47601, model.VLESS, "",
		`{"clients":[],"decryption":"none"}`)

	s := webBackendService(t)
	ib := &model.Inbound{
		Tag: "mtproto-1", Protocol: model.MTProto,
		Settings: `{"webBackendEnable":true,"webBackendPort":47601}`,
	}
	if err := s.normalizeWebBackendPort(ib, `{"webBackendEnable":true,"webBackendPort":47601}`); err != nil {
		t.Fatal(err)
	}
	p := webBackendPortOf(t, ib.Settings)
	if p == 47601 {
		t.Fatal("a port an inbound already binds on 127.0.0.1 must be replaced, not stored")
	}
	if p <= 0 {
		t.Fatalf("a replacement port must still be allocated, got %d", p)
	}
}

// The allocator has to be reached on the save path, not merely exist: the
// operator switch is a settings key nothing else acts on.
func TestSavingAnInboundAllocatesAndKeepsTheWebBackendPort(t *testing.T) {
	setupConflictDB(t)
	const clients = `"clients":[{"email":"web@mt","enable":true,"secret":"ee0102030405060708090a0b0c0d0e0f"}]`

	svc := &InboundService{}
	created, _, _, err := svc.AddInbound(&model.Inbound{
		UserId: 1, Enable: true, Port: 20441, Protocol: model.MTProto, Tag: "mtproto-web",
		Listen: "0.0.0.0", Settings: `{"webBackendEnable":true,` + clients + `}`,
	})
	if err != nil {
		t.Fatalf("AddInbound: %v", err)
	}
	allocated := webBackendPortOf(t, created.Settings)
	if allocated <= 0 {
		t.Fatalf("AddInbound must allocate the WEB backend port, got %d in %s", allocated, created.Settings)
	}

	// The client never echoes the port back here; the stored one must survive.
	edited := *created
	edited.Settings = `{"webBackendEnable":true,` + clients + `}`
	updated, _, _, err := svc.UpdateInbound(&edited)
	if err != nil {
		t.Fatalf("UpdateInbound: %v", err)
	}
	if got := webBackendPortOf(t, updated.Settings); got != allocated {
		t.Fatalf("a second save must keep the port %d, got %d", allocated, got)
	}
}
