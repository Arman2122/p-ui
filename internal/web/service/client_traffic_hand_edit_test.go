package service

import "testing"

/*
Setting a depleted client's counters by hand brings the client back.

POST /panel/api/clients/updateTraffic/:email writes up/down directly. Every
reset path snapshots who was depleted before zeroing and re-enables whoever is
under quota afterwards; this path wrote the numbers and stopped, so an operator
who lowered a client's usage saw a client under its quota that the core still
refused. Same snapshot, same re-enable, plus the daemon-side counter reset the
other paths do. Feature gap C1.
*/
func TestHandEditingCountersReEnablesADepletedClient(t *testing.T) {
	initTestDB(t)
	const email = "hand@x"
	inbound := seedDepletableClient(t, 29011, "in-hand-edit", email)

	svc := &InboundService{}
	if _, _, err := svc.AddTraffic(nil, nil); err != nil {
		t.Fatalf("AddTraffic: %v", err)
	}
	if got := readEnableLocations(t, inbound.Id, email); got.traffic || got.client || got.settings {
		t.Fatalf("depletion left the client enabled somewhere: %+v", got)
	}

	if err := svc.UpdateClientTrafficByEmail(email, 10, 10); err != nil {
		t.Fatalf("UpdateClientTrafficByEmail: %v", err)
	}
	got := readEnableLocations(t, inbound.Id, email)
	if !got.traffic || !got.client || !got.settings {
		t.Fatalf("the client is under its quota again and still cut off: client_traffics=%v clients=%v settings=%v",
			got.traffic, got.client, got.settings)
	}
}

// A hand edit that keeps the client over its quota must not revive it.
func TestHandEditingCountersLeavesAStillDepletedClientOff(t *testing.T) {
	initTestDB(t)
	const email = "still@x"
	inbound := seedDepletableClient(t, 29012, "in-hand-still", email)
	svc := &InboundService{}
	if _, _, err := svc.AddTraffic(nil, nil); err != nil {
		t.Fatalf("AddTraffic: %v", err)
	}
	if err := svc.UpdateClientTrafficByEmail(email, 90, 90); err != nil {
		t.Fatalf("UpdateClientTrafficByEmail: %v", err)
	}
	if got := readEnableLocations(t, inbound.Id, email); got.traffic || got.client || got.settings {
		t.Fatalf("a client still over its quota was re-enabled: %+v", got)
	}
}
