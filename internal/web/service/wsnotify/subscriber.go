/*
Package wsnotify forwards event-bus events to open browsers.

Until this existed the bus had exactly two subscribers, email and Telegram, so a
core crash or a node going offline was invisible to anyone actually looking at
the panel. This is the third subscriber: it renders nothing, it only names the
i18n key and the parameters, because the browser is the side that knows which
locale to draw.
*/
package wsnotify

import (
	"strconv"
	"time"

	"github.com/Arman2122/p-ui/internal/eventbus"
	"github.com/Arman2122/p-ui/internal/web/websocket"
)

// One notification per event kind per source per minute. The watchdogs and the
// heartbeat both run on a one-minute cadence, so a flapping node cannot fill the
// bell faster than an operator can read it.
const cooldown = time.Minute

type Subscriber struct {
	limiter *eventbus.RateLimiter
}

func New() *Subscriber {
	return &Subscriber{limiter: eventbus.NewRateLimiter(cooldown)}
}

type notice struct {
	level    string
	titleKey string
	bodyKey  string
	params   map[string]string
}

func (s *Subscriber) HandleEvent(e eventbus.Event) {
	// Serialising an event nobody will receive is pure waste.
	if !websocket.HasClients() {
		return
	}
	n, ok := describe(e)
	if !ok {
		return
	}
	if !s.limiter.Allow(e.Type, eventbus.LimiterKey(e)) {
		return
	}
	websocket.BroadcastEvent(n.level, n.titleKey, n.bodyKey, n.params)
}

/*
cpu.high and memory.high are deliberately absent. Their jobs publish on every
run rather than only when high, so the threshold lives in the subscriber — and
the dashboard already shows both readings live, refreshed every two seconds.
A bell entry for them would be noise over a number already on screen.
*/
func describe(e eventbus.Event) (notice, bool) {
	switch e.Type {
	case eventbus.EventOutboundDown:
		msg := ""
		if d, ok := e.Data.(*eventbus.OutboundHealthData); ok {
			msg = d.Error
		}
		return notice{"error", "events.outboundDown.title", "events.outboundDown.body",
			map[string]string{"tag": e.Source, "error": msg}}, true

	case eventbus.EventOutboundUp:
		delay := ""
		if d, ok := e.Data.(*eventbus.OutboundHealthData); ok && d.Delay > 0 {
			delay = strconv.FormatInt(d.Delay, 10)
		}
		return notice{"success", "events.outboundUp.title", "events.outboundUp.body",
			map[string]string{"tag": e.Source, "delay": delay}}, true

	case eventbus.EventCoreCrash:
		core, msg := e.Source, ""
		if d, ok := e.Data.(*eventbus.CoreCrashData); ok {
			if d.CoreID != "" {
				core = d.CoreID
			}
			msg = d.Err
		}
		return notice{"error", "events.coreCrash.title", "events.coreCrash.body",
			map[string]string{"core": core, "error": msg}}, true

	case eventbus.EventCoreUp:
		core := e.Source
		if d, ok := e.Data.(*eventbus.CoreCrashData); ok && d.CoreID != "" {
			core = d.CoreID
		}
		return notice{"success", "events.coreUp.title", "events.coreUp.body",
			map[string]string{"core": core}}, true

	case eventbus.EventXrayCrash:
		msg, _ := e.Data.(string)
		return notice{"error", "events.xrayCrash.title", "events.xrayCrash.body",
			map[string]string{"error": msg}}, true

	case eventbus.EventNodeDown:
		return notice{"error", "events.nodeDown.title", "events.nodeDown.body",
			map[string]string{"node": e.Source}}, true

	case eventbus.EventNodeUp:
		latency := ""
		if d, ok := e.Data.(*eventbus.NodeHealthData); ok && d.LatencyMs > 0 {
			latency = strconv.Itoa(d.LatencyMs)
		}
		return notice{"success", "events.nodeUp.title", "events.nodeUp.body",
			map[string]string{"node": e.Source, "latency": latency}}, true

	case eventbus.EventLoginAttempt:
		d, ok := e.Data.(*eventbus.LoginEventData)
		if !ok {
			return notice{}, false
		}
		params := map[string]string{"user": d.Username, "ip": d.IP}
		if d.Status == "success" {
			return notice{"success", "events.loginSuccess.title", "events.loginSuccess.body", params}, true
		}
		return notice{"warning", "events.loginFail.title", "events.loginFail.body", params}, true

	case eventbus.EventCoreAuthFailure:
		d, ok := e.Data.(*eventbus.CoreAuthFailureData)
		if !ok {
			return notice{}, false
		}
		return notice{"warning", "events.coreAuthFailure.title", "events.coreAuthFailure.body",
			map[string]string{"core": d.CoreID, "user": d.Username, "ip": d.IP}}, true

	case eventbus.EventClientDeviceLimit:
		d, ok := e.Data.(*eventbus.ClientDeviceLimitData)
		if !ok {
			return notice{}, false
		}
		return notice{"warning", "events.deviceLimit.title", "events.deviceLimit.body",
			map[string]string{
				"email":   d.Email,
				"devices": strconv.Itoa(d.Devices),
				"limit":   strconv.Itoa(d.Limit),
				"ip":      d.IP,
			}}, true
	}
	return notice{}, false
}
