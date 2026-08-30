package service

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
The three enable locations, read back the way the core reads them.

client_traffics gates both render paths, the clients row gates RenderInbound (so
every Xray kind) and the inbound's settings JSON gates buildInboundForLocalRuntime
(so mtproto and wgkernel). The core skips a client that is false in any of them,
which is why asserting one of the three is how this bug survived a test.
*/
type enableLocations struct {
	traffic  bool
	client   bool
	settings bool
}

func readEnableLocations(t *testing.T, inboundID int, email string) enableLocations {
	t.Helper()
	db := database.GetDB()

	var traffic core.ClientTraffic
	if err := db.Model(core.ClientTraffic{}).Where("email = ?", email).First(&traffic).Error; err != nil {
		t.Fatalf("read client_traffics for %s: %v", email, err)
	}
	var record model.ClientRecord
	if err := db.Model(model.ClientRecord{}).Where("email = ?", email).First(&record).Error; err != nil {
		t.Fatalf("read clients for %s: %v", email, err)
	}
	var inbound model.Inbound
	if err := db.First(&inbound, inboundID).Error; err != nil {
		t.Fatalf("read inbound %d: %v", inboundID, err)
	}
	var settings map[string]any
	if err := json.Unmarshal([]byte(inbound.Settings), &settings); err != nil {
		t.Fatalf("parse settings of inbound %d: %v", inboundID, err)
	}
	found := enableLocations{traffic: traffic.Enable, client: record.Enable}
	clients, _ := settings["clients"].([]any)
	for _, raw := range clients {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if addr, _ := entry["email"].(string); addr != email {
			continue
		}
		enabled, present := entry["enable"].(bool)
		found.settings = !present || enabled
	}
	return found
}

// seedDepletableClient creates an inbound with one client that has spent its
// whole allowance, which is the state the depletion pass acts on.
func seedDepletableClient(t *testing.T, port int, tag, email string) *model.Inbound {
	t.Helper()
	db := database.GetDB()

	settings, err := json.Marshal(map[string]any{
		"clients": []any{map[string]any{
			"id": "11111111-2222-3333-4444-555555555555", "email": email,
			"enable": true, "totalGB": 1,
		}},
		"decryption": "none",
	})
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	inbound := &model.Inbound{
		UserId: 1, Enable: true, Tag: tag, Port: port, Protocol: model.VLESS,
		Listen: "0.0.0.0", Settings: string(settings),
		StreamSettings: `{"network":"tcp","security":"none"}`,
	}
	if _, _, _, err := (&InboundService{}).AddInbound(inbound); err != nil {
		t.Fatalf("AddInbound: %v", err)
	}
	if err := db.Model(core.ClientTraffic{}).Where("email = ?", email).
		Updates(map[string]any{"total": 100, "up": 60, "down": 60}).Error; err != nil {
		t.Fatalf("spend the allowance: %v", err)
	}
	return inbound
}

/*
Every reset path, one lifecycle each: deplete, let the depletion pass cut the
client off in all three places, reset through that path, and read all three back.

Each of these restored client_traffics and nothing else, so the panel showed a
fresh allowance while the core still had no such client. They share a helper now,
and this is what stops a "bulk efficiency" shortcut creeping back into one of
them: the shortcut is what the bug was.
*/
func TestEveryResetPathRestoresAllThreeEnableLocations(t *testing.T) {
	tests := []struct {
		name  string
		port  int
		tag   string
		email string
		reset func(t *testing.T, inbound *model.Inbound, email string)
	}{
		{
			name: "the periodic reset job", port: 29001, tag: "in-periodic", email: "periodic@x",
			reset: func(t *testing.T, inbound *model.Inbound, _ string) {
				if err := (&ClientService{}).ResetAllClientTraffics(&InboundService{}, inbound.Id); err != nil {
					t.Fatalf("ResetAllClientTraffics: %v", err)
				}
			},
		},
		{
			name: "the Clients page reset-all button", port: 29002, tag: "in-all", email: "all@x",
			reset: func(t *testing.T, _ *model.Inbound, _ string) {
				if _, err := (&ClientService{}).ResetAllTraffics(&InboundService{}); err != nil {
					t.Fatalf("ResetAllTraffics: %v", err)
				}
			},
		},
		{
			name: "the Telegram bot's reset traffic", port: 29003, tag: "in-bot", email: "bot@x",
			reset: func(t *testing.T, _ *model.Inbound, email string) {
				if err := (&InboundService{}).ResetClientTrafficByEmail(email); err != nil {
					t.Fatalf("ResetClientTrafficByEmail: %v", err)
				}
			},
		},
		{
			name: "the per-client reset, which already worked", port: 29004, tag: "in-single", email: "single@x",
			reset: func(t *testing.T, _ *model.Inbound, email string) {
				if _, err := (&ClientService{}).ResetTrafficByEmail(&InboundService{}, email); err != nil {
					t.Fatalf("ResetTrafficByEmail: %v", err)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			initTestDB(t)
			inbound := seedDepletableClient(t, tc.port, tc.tag, tc.email)

			svc := &InboundService{}
			// Through AddTraffic, not disableInvalidClients: the settings-JSON sync
			// now happens after the traffic transaction commits, under lockInbound.
			// Calling the sub-step directly would assert on a half-finished pass.
			if _, _, err := svc.AddTraffic(nil, nil); err != nil {
				t.Fatalf("AddTraffic: %v", err)
			}
			if got := readEnableLocations(t, inbound.Id, tc.email); got.traffic || got.client || got.settings {
				t.Fatalf("depletion left the client enabled somewhere: %+v", got)
			}

			tc.reset(t, inbound, tc.email)

			got := readEnableLocations(t, inbound.Id, tc.email)
			if !got.traffic || !got.client || !got.settings {
				t.Fatalf("after the reset the client is still cut off: client_traffics=%v clients=%v settings=%v",
					got.traffic, got.client, got.settings)
			}
		})
	}
}

/*
The rule that decides who comes back, stated as two cases the panel must not
confuse: a client the operator disabled by hand and who is NOT depleted stays
disabled through a reset, because zeroing usage is not a reason to undo an
administrative decision.

The other corner — disabled by hand AND depleted — does come back, deliberately,
which is the answer the per-client paths have always given.
*/
func TestResetLeavesAnAdminDisabledClientDisabled(t *testing.T) {
	initTestDB(t)
	inbound := seedDepletableClient(t, 29010, "in-admin", "admin-off@x")

	db := database.GetDB()
	// Not depleted: give the allowance back before disabling by hand.
	if err := db.Model(core.ClientTraffic{}).Where("email = ?", "admin-off@x").
		Updates(map[string]any{"up": 0, "down": 0, "enable": false}).Error; err != nil {
		t.Fatalf("seed traffic: %v", err)
	}
	if err := db.Model(model.ClientRecord{}).Where("email = ?", "admin-off@x").
		Update("enable", false).Error; err != nil {
		t.Fatalf("seed clients row: %v", err)
	}

	if err := (&ClientService{}).ResetAllClientTraffics(&InboundService{}, inbound.Id); err != nil {
		t.Fatalf("ResetAllClientTraffics: %v", err)
	}

	if got := readEnableLocations(t, inbound.Id, "admin-off@x"); got.traffic || got.client {
		t.Fatalf("a quota reset re-enabled a client the operator had disabled: %+v", got)
	}
}

// Expiry does not move when usage is zeroed, so a client past its expiry stays
// disabled — the re-enable re-checks depletion rather than assuming the reset
// cleared it.
func TestResetDoesNotReviveAnExpiredClient(t *testing.T) {
	initTestDB(t)
	inbound := seedDepletableClient(t, 29011, "in-expired", "expired@x")

	db := database.GetDB()
	if err := db.Model(core.ClientTraffic{}).Where("email = ?", "expired@x").
		Update("expiry_time", time.Now().Add(-time.Hour).UnixMilli()).Error; err != nil {
		t.Fatalf("seed expiry: %v", err)
	}
	if _, _, err := (&InboundService{}).AddTraffic(nil, nil); err != nil {
		t.Fatalf("AddTraffic: %v", err)
	}

	if err := (&ClientService{}).ResetAllClientTraffics(&InboundService{}, inbound.Id); err != nil {
		t.Fatalf("ResetAllClientTraffics: %v", err)
	}

	if got := readEnableLocations(t, inbound.Id, "expired@x"); got.traffic || got.client {
		t.Fatalf("a traffic reset revived a client that is past its expiry: %+v", got)
	}
}

// A fresh struct per read: GORM folds a populated primary key into the query
// conditions, so reusing one silently turns the next lookup into a no-match.
func reloadInbound(t *testing.T, id int) model.Inbound {
	t.Helper()
	var row model.Inbound
	if err := database.GetDB().First(&row, id).Error; err != nil {
		t.Fatalf("reload inbound %d: %v", id, err)
	}
	return row
}

/*
F028: an inbound carries its own quota, and its own reset has to be able to undo
its own depletion — but only its own. An inbound the operator disabled by hand
stays disabled.
*/
func TestResetInboundTrafficRestoresAnInboundItsQuotaDisabled(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()
	svc := &InboundService{}

	depleted := seedDepletableClient(t, 29020, "in-quota", "quota@x")
	if err := db.Model(model.Inbound{}).Where("id = ?", depleted.Id).
		Updates(map[string]any{"total": 100, "up": 60, "down": 60}).Error; err != nil {
		t.Fatalf("spend the inbound allowance: %v", err)
	}
	byHand := seedDepletableClient(t, 29021, "in-byhand", "byhand@x")
	if err := db.Model(model.Inbound{}).Where("id = ?", byHand.Id).
		Update("enable", false).Error; err != nil {
		t.Fatalf("disable by hand: %v", err)
	}

	if _, _, _, err := svc.disableInvalidInbounds(db); err != nil {
		t.Fatalf("disableInvalidInbounds: %v", err)
	}
	if reloadInbound(t, depleted.Id).Enable {
		t.Fatal("the inbound was not disabled by its own quota; the rest of this test proves nothing")
	}

	if err := svc.ResetInboundTraffic(depleted.Id); err != nil {
		t.Fatalf("ResetInboundTraffic: %v", err)
	}
	if err := svc.ResetInboundTraffic(byHand.Id); err != nil {
		t.Fatalf("ResetInboundTraffic on the hand-disabled inbound: %v", err)
	}

	if !reloadInbound(t, depleted.Id).Enable {
		t.Fatal("an inbound disabled by its own quota is still disabled after its own reset")
	}
	if reloadInbound(t, byHand.Id).Enable {
		t.Fatal("a traffic reset re-enabled an inbound the operator had disabled by hand")
	}
}
