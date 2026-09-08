package wsnotify

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/eventbus"
)

func TestDescribeMapsEachForwardedEvent(t *testing.T) {
	tests := []struct {
		name     string
		event    eventbus.Event
		level    string
		titleKey string
		bodyKey  string
		params   map[string]string
	}{
		{
			name:     "outbound down carries the probe error",
			event:    eventbus.Event{Type: eventbus.EventOutboundDown, Source: "warp", Data: &eventbus.OutboundHealthData{Error: "i/o timeout"}},
			level:    "error",
			titleKey: "events.outboundDown.title",
			bodyKey:  "events.outboundDown.body",
			params:   map[string]string{"tag": "warp", "error": "i/o timeout"},
		},
		{
			name:     "outbound up carries the measured delay",
			event:    eventbus.Event{Type: eventbus.EventOutboundUp, Source: "warp", Data: &eventbus.OutboundHealthData{Delay: 62}},
			level:    "success",
			titleKey: "events.outboundUp.title",
			bodyKey:  "events.outboundUp.body",
			params:   map[string]string{"tag": "warp", "delay": "62"},
		},
		{
			name:     "core crash prefers the payload's core id over the source",
			event:    eventbus.Event{Type: eventbus.EventCoreCrash, Source: "ignored", Data: &eventbus.CoreCrashData{CoreID: "singbox", Err: "exit status 1"}},
			level:    "error",
			titleKey: "events.coreCrash.title",
			bodyKey:  "events.coreCrash.body",
			params:   map[string]string{"core": "singbox", "error": "exit status 1"},
		},
		{
			name:     "xray crash carries the error string payload",
			event:    eventbus.Event{Type: eventbus.EventXrayCrash, Data: "signal: killed"},
			level:    "error",
			titleKey: "events.xrayCrash.title",
			bodyKey:  "events.xrayCrash.body",
			params:   map[string]string{"error": "signal: killed"},
		},
		{
			name:     "node down names the node",
			event:    eventbus.Event{Type: eventbus.EventNodeDown, Source: "fra-02"},
			level:    "error",
			titleKey: "events.nodeDown.title",
			bodyKey:  "events.nodeDown.body",
			params:   map[string]string{"node": "fra-02"},
		},
		{
			name:     "a successful login is not an alert",
			event:    eventbus.Event{Type: eventbus.EventLoginAttempt, Data: &eventbus.LoginEventData{Username: "admin", IP: "1.2.3.4", Status: "success"}},
			level:    "success",
			titleKey: "events.loginSuccess.title",
			bodyKey:  "events.loginSuccess.body",
			params:   map[string]string{"user": "admin", "ip": "1.2.3.4"},
		},
		{
			name:     "a failed login is a warning",
			event:    eventbus.Event{Type: eventbus.EventLoginAttempt, Data: &eventbus.LoginEventData{Username: "root", IP: "9.9.9.9", Status: "fail"}},
			level:    "warning",
			titleKey: "events.loginFail.title",
			bodyKey:  "events.loginFail.body",
			params:   map[string]string{"user": "root", "ip": "9.9.9.9"},
		},
		{
			name:     "device limit reports the count against the cap",
			event:    eventbus.Event{Type: eventbus.EventClientDeviceLimit, Data: &eventbus.ClientDeviceLimitData{Email: "roham", IP: "5.5.5.5", Limit: 2, Devices: 4}},
			level:    "warning",
			titleKey: "events.deviceLimit.title",
			bodyKey:  "events.deviceLimit.body",
			params:   map[string]string{"email": "roham", "devices": "4", "limit": "2", "ip": "5.5.5.5"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := describe(tc.event)
			if !ok {
				t.Fatalf("describe(%s) returned ok=false, want a notice", tc.event.Type)
			}
			if got.level != tc.level {
				t.Errorf("level = %q, want %q", got.level, tc.level)
			}
			if got.titleKey != tc.titleKey {
				t.Errorf("titleKey = %q, want %q", got.titleKey, tc.titleKey)
			}
			if got.bodyKey != tc.bodyKey {
				t.Errorf("bodyKey = %q, want %q", got.bodyKey, tc.bodyKey)
			}
			if len(got.params) != len(tc.params) {
				t.Fatalf("params = %v, want %v", got.params, tc.params)
			}
			for k, want := range tc.params {
				if got.params[k] != want {
					t.Errorf("params[%q] = %q, want %q", k, got.params[k], want)
				}
			}
		})
	}
}

/*
The watchdogs publish on every run, not only when the reading is high, and the
threshold lives in the SMTP settings. Forwarding them here would put a bell entry
on screen every minute for a number the dashboard already shows live.
*/
func TestDescribeDropsTheUnthresholdedWatchdogEvents(t *testing.T) {
	for _, typ := range []eventbus.EventType{eventbus.EventCPUHigh, eventbus.EventMemoryHigh} {
		t.Run(string(typ), func(t *testing.T) {
			if _, ok := describe(eventbus.Event{Type: typ, Data: &eventbus.SystemMetricData{Percent: 99}}); ok {
				t.Fatalf("describe(%s) forwarded the event; the job publishes it every run regardless of the threshold", typ)
			}
		})
	}
}

func TestDescribeIgnoresAnEventItDoesNotKnow(t *testing.T) {
	if _, ok := describe(eventbus.Event{Type: eventbus.EventType("something.new")}); ok {
		t.Fatal("describe forwarded an unknown event type")
	}
}

// A payload of the wrong type must be dropped, not panic the publishing goroutine.
func TestDescribeSurvivesAMistypedPayload(t *testing.T) {
	for _, typ := range []eventbus.EventType{
		eventbus.EventLoginAttempt,
		eventbus.EventCoreAuthFailure,
		eventbus.EventClientDeviceLimit,
	} {
		t.Run(string(typ), func(t *testing.T) {
			if _, ok := describe(eventbus.Event{Type: typ, Data: "not the struct"}); ok {
				t.Fatalf("describe(%s) accepted a mistyped payload", typ)
			}
		})
	}
}

func TestOutboundDownSurvivesANilPayload(t *testing.T) {
	got, ok := describe(eventbus.Event{Type: eventbus.EventOutboundDown, Source: "direct"})
	if !ok {
		t.Fatal("an outbound going down should still be reported without probe detail")
	}
	if got.params["tag"] != "direct" || got.params["error"] != "" {
		t.Errorf("params = %v, want tag=direct with an empty error", got.params)
	}
}
