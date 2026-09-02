package service

import (
	"fmt"
	"sync"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/eventbus"
)

// subscriptionAlarm publishes outbound.down once per failing subscription and
// outbound.up once it fetches again, so a feed that stays broken is not re-announced.
type subscriptionAlarm struct {
	mu   sync.Mutex
	down map[int]bool
}

var subscriptionAlarms = &subscriptionAlarm{down: map[int]bool{}}

// failed reports whether this failure is the first since the last success.
func (a *subscriptionAlarm) failed(id int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.down[id] {
		return false
	}
	a.down[id] = true
	return true
}

// recovered reports whether a failure had been announced for this subscription.
func (a *subscriptionAlarm) recovered(id int) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.down[id] {
		return false
	}
	delete(a.down, id)
	return true
}

func subscriptionEventSource(sub *model.OutboundSubscription) string {
	if sub.Remark != "" {
		return sub.Remark
	}
	return fmt.Sprintf("subscription #%d", sub.Id)
}

func announceSubscriptionFailure(sub *model.OutboundSubscription, err error) {
	if eventBus == nil || !subscriptionAlarms.failed(sub.Id) {
		return
	}
	eventBus.Publish(eventbus.Event{
		Type:   eventbus.EventOutboundDown,
		Source: subscriptionEventSource(sub),
		Data:   &eventbus.OutboundHealthData{Error: err.Error()},
	})
}

func announceSubscriptionRecovery(sub *model.OutboundSubscription) {
	if eventBus == nil || !subscriptionAlarms.recovered(sub.Id) {
		return
	}
	eventBus.Publish(eventbus.Event{
		Type:   eventbus.EventOutboundUp,
		Source: subscriptionEventSource(sub),
		Data:   &eventbus.OutboundHealthData{},
	})
}
