package service

import (
	"errors"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/eventbus"
)

/*
A failing subscription fetch stored last_error and stopped there, so a feed
that had been dead for a week raised nothing while a single outbound probe
raised an alert. The first failure now publishes outbound.down, a later
success outbound.up, and a feed that stays down is not re-announced on every
refresh. Feature gap E7.
*/
func TestSubscriptionFailureIsAnnouncedOnceUntilItRecovers(t *testing.T) {
	prev := eventBus
	bus := eventbus.New(8)
	SetEventBus(bus)
	t.Cleanup(func() {
		bus.Stop()
		SetEventBus(prev)
	})
	subscriptionAlarms = &subscriptionAlarm{down: map[int]bool{}}

	got := make(chan eventbus.Event, 8)
	bus.Subscribe("test", func(e eventbus.Event) { got <- e })

	sub := &model.OutboundSubscription{Id: 41, Remark: "mirror feed"}
	announceSubscriptionFailure(sub, errors.New("dial tcp: connection refused"))
	announceSubscriptionFailure(sub, errors.New("dial tcp: connection refused"))
	announceSubscriptionRecovery(sub)
	announceSubscriptionRecovery(sub)

	var events []eventbus.Event
	for len(events) < 2 {
		select {
		case e := <-got:
			events = append(events, e)
		case <-time.After(2 * time.Second):
			t.Fatalf("received %d events, want down then up: %+v", len(events), events)
		}
	}
	select {
	case e := <-got:
		t.Fatalf("a third event was published: %+v", e)
	case <-time.After(200 * time.Millisecond):
	}
	if events[0].Type != eventbus.EventOutboundDown || events[0].Source != "mirror feed" {
		t.Fatalf("first event = %+v, want outbound.down for the feed", events[0])
	}
	if data, ok := events[0].Data.(*eventbus.OutboundHealthData); !ok || data.Error != "dial tcp: connection refused" {
		t.Fatalf("down event carries %+v, want the fetch error", events[0].Data)
	}
	if events[1].Type != eventbus.EventOutboundUp || events[1].Source != "mirror feed" {
		t.Fatalf("second event = %+v, want outbound.up for the feed", events[1])
	}
}
