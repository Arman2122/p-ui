package eventbus_test

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/eventbus"
)

/*
A core's daemon dying has to be an EVENT, not a log line.

Only Xray had a death signal: xray.OnCrash published, and everything downstream
switched on that one type. An mtg sidecar exiting logged and returned — one
inbound's clients offline with no event, no notification and nothing on the
dashboard. This pins the generic shape both cores now publish.
*/
func TestCoreCrashCarriesWhoDied(t *testing.T) {
	bus := eventbus.New(8)
	t.Cleanup(bus.Stop)

	got := make(chan eventbus.Event, 1)
	bus.Subscribe("test", func(e eventbus.Event) {
		if e.Type == eventbus.EventCoreCrash {
			got <- e
		}
	})

	bus.Publish(eventbus.Event{
		Type:   eventbus.EventCoreCrash,
		Source: "mtproto inbound 7",
		Data: &eventbus.CoreCrashData{
			CoreID: "mtproto", InstanceID: 7, Err: "signal: killed",
		},
	})

	e := <-got
	data, ok := e.Data.(*eventbus.CoreCrashData)
	if !ok {
		t.Fatalf("Data = %T, want *CoreCrashData; a notifier cannot name the core without it", e.Data)
	}
	if data.CoreID != "mtproto" {
		t.Errorf("CoreID = %q, want mtproto", data.CoreID)
	}
	/* Instance-aware because the two process shapes both ship: mtg runs one per
	   inbound, so "mtproto crashed" without the id names no actionable thing. */
	if data.InstanceID != 7 {
		t.Errorf("InstanceID = %d, want 7", data.InstanceID)
	}
	if data.Err != "signal: killed" {
		t.Errorf("Err = %q, want the daemon's own reason", data.Err)
	}
	if e.Source == "" {
		t.Error("Source is empty; it is what an operator reads in the notification")
	}
}
