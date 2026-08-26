package service

import (
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
A depletion must not be undone by a concurrent add, permanently.

inbounds.settings is a JSON blob that every writer reads, edits and writes back
whole. Admin mutations do that under lockInbound; the depletion pass did it
inside the traffic transaction with no lock at all. So an add that had already
read the blob saved its own copy afterwards and restored the depleted client's
`"enable": true`.

The damage does not heal. The add's SyncInbound flips clients.enable back too,
and the depleted predicate only ever selects rows still enabled in
client_traffics — which the poll had already flipped false. No later poll
re-detects the client, both places the config builders read say enabled, and an
exhausted quota is served until someone resets that client's traffic by hand.
F381.

Staged as the real interleaving: the add reads the settings blob, the depletion
pass runs to completion, and only then does the add write its stale copy back.
*/
func TestTheDeferredDisableMergesWithAConcurrentAdd(t *testing.T) {
	initTestDB(t)
	inbound := seedDepletableClient(t, 24801, "lost-update-in", "depleted@x")
	db := database.GetDB()

	// A client added while the poll is deciding. Written the way the admin path
	// writes it: the whole settings blob, replaced.
	var current model.Inbound
	if err := db.Where("id = ?", inbound.Id).First(&current).Error; err != nil {
		t.Fatalf("read settings: %v", err)
	}
	// The stored blob is pretty-printed, so splice on the opening bracket wherever
	// the encoder put it rather than assuming a shape.
	marker := `"clients": [`
	if !strings.Contains(current.Settings, marker) {
		marker = `"clients":[`
	}
	withNewClient := strings.Replace(current.Settings, marker,
		marker+`{"id":"11111111-2222-4333-8444-555555555555","email":"added@x","enable":true},`, 1)
	if withNewClient == current.Settings {
		t.Fatalf("could not stage a second client into %q", current.Settings)
	}
	if err := db.Model(&model.Inbound{}).Where("id = ?", inbound.Id).
		Update("settings", withNewClient).Error; err != nil {
		t.Fatalf("stage the add: %v", err)
	}

	if _, _, err := (&InboundService{}).AddTraffic(nil, nil); err != nil {
		t.Fatalf("AddTraffic: %v", err)
	}

	/*
		Both must be true afterwards. The depletion is applied to the blob as it
		stands NOW, not to the copy the pass read when it started — which is what
		re-reading under lockInbound buys. A write-back of the pre-add snapshot
		would have dropped the new client; a write-back of the pre-depletion
		snapshot would have re-enabled the depleted one.
	*/
	var after model.Inbound
	if err := db.Where("id = ?", inbound.Id).First(&after).Error; err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !strings.Contains(after.Settings, "added@x") {
		t.Fatalf("the concurrent add was overwritten by the depletion pass: %s", after.Settings)
	}
	if got := readEnableLocations(t, inbound.Id, "depleted@x"); got.settings {
		t.Fatalf("the depleted client is still enabled in settings.JSON: %s", after.Settings)
	}
}

/*
And an operator's reset in the window between commit and sync is not undone.

The deferred write re-checks that the depletion still stands. Without that, a
reset landing in exactly that gap would be reversed — and with usage zeroed
nothing would ever re-deplete the client, so it would sit disabled with a full
allowance until someone noticed.
*/
func TestAResetDuringTheDeferredSyncIsNotUndone(t *testing.T) {
	initTestDB(t)
	inbound := seedDepletableClient(t, 24802, "reset-window-in", "resettable@x")

	pending := pendingClientDisable{
		local: map[int]map[string]struct{}{
			inbound.Id: {"resettable@x": {}},
		},
	}

	// The operator resets before the deferred half runs.
	if err := (&ClientService{}).ResetAllClientTraffics(&InboundService{}, inbound.Id); err != nil {
		t.Fatalf("reset: %v", err)
	}

	(&InboundService{}).applyPendingClientDisables(pending)

	if got := readEnableLocations(t, inbound.Id, "resettable@x"); got.settings == false {
		t.Fatal("a client the operator had just reset was disabled anyway; with its usage zeroed nothing will re-deplete it")
	}
}

// The deferred work must take lockInbound BEFORE the serial writer, which is the
// order every admin mutation uses. The other order deadlocks the panel: an admin
// mutation holds the lock while waiting for the writer, and the writer is what
// runs the traffic pass.
func TestTheDeferredDisableTakesTheLockBeforeTheWriter(t *testing.T) {
	body := readServiceSource(t, "pending_client_disable.go")
	apply := body[strings.Index(body, "func (s *InboundService) applyOneDisable"):]
	apply = apply[:strings.Index(apply, "\n}\n")]

	lock := strings.Index(apply, "lockInbound(inboundID)")
	writer := strings.Index(apply, "runSerializedTx(")
	if lock < 0 || writer < 0 {
		t.Fatal("applyOneDisable no longer locks or no longer writes; this guard is now vacuous")
	}
	if lock > writer {
		t.Fatal("lockInbound is taken inside the serial writer: an admin mutation holding that lock while waiting for the writer deadlocks the panel, and no timeout covers it")
	}

	// And it must not have crept back into the transaction it was moved out of.
	disable := readServiceSource(t, "inbound_disable.go")
	scan := disable[strings.Index(disable, "func (s *InboundService) disableInvalidClients"):]
	scan = scan[:strings.Index(scan, "\n// markClientsDisabledInSettings")]
	if strings.Contains(scan, "s.markClientsDisabledInSettings(tx") {
		t.Fatal("the settings write is back inside the traffic transaction, unlocked — the lost update F381 describes")
	}
}
