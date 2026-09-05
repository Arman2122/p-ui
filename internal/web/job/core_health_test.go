package job

import (
	"context"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/eventbus"
)

// healthCore is a supervised core whose health the test scripts pass by pass.
type healthCore struct {
	recordingCore
	health core.Health
}

func (c *healthCore) Health(context.Context) (core.Health, error) { return c.health, nil }

func collect(t *testing.T, bus *eventbus.Bus) <-chan eventbus.Event {
	t.Helper()
	got := make(chan eventbus.Event, 16)
	bus.Subscribe("test", func(e eventbus.Event) {
		if e.Type == eventbus.EventCoreCrash || e.Type == eventbus.EventCoreUp {
			got <- e
		}
	})
	return got
}

func expectEvent(t *testing.T, got <-chan eventbus.Event, want eventbus.EventType, source string) eventbus.Event {
	t.Helper()
	select {
	case e := <-got:
		if e.Type != want || e.Source != source {
			t.Fatalf("event = %s from %q, want %s from %q", e.Type, e.Source, want, source)
		}
		return e
	case <-time.After(2 * time.Second):
		t.Fatalf("no %s event for %q", want, source)
	}
	return eventbus.Event{}
}

func expectQuiet(t *testing.T, got <-chan eventbus.Event) {
	t.Helper()
	select {
	case e := <-got:
		t.Fatalf("unexpected %s from %q: a steady pass, or a second pass of the same outage, must publish nothing", e.Type, e.Source)
	case <-time.After(300 * time.Millisecond):
	}
}

/*
A crash is one event per outage and its all-clear one event, whichever backend
runs the daemon: exec reports a dead unit as failed until the pass repairs it;
systemd restarts it itself and only Restarts says so. A crash loop is not one
alert per pass, and a steady daemon is none.
*/
func TestACrashIsPublishedOnceAndItsRecoveryOnce(t *testing.T) {
	bus := eventbus.New(16)
	t.Cleanup(bus.Stop)
	EventBus = bus
	t.Cleanup(func() { EventBus = nil })
	got := collect(t, bus)

	c := &healthCore{recordingCore: recordingCore{id: "daemon", kind: "daemonkind"}}
	bound := core.Bind(c)
	j := NewCoreSuperviseJob("xray")
	ctx := context.Background()
	up := core.InstanceHealth{InstanceID: 3, Tag: "d-3", State: core.HealthRunning, Ready: true}

	c.health = core.Health{Instances: []core.InstanceHealth{up}}
	j.observeHealth(ctx, bound)
	expectQuiet(t, got)

	// exec: the unit died and stays failed until the reconcile after this pass.
	c.health = core.Health{Instances: []core.InstanceHealth{{InstanceID: 3, Tag: "d-3", State: core.HealthFailed, Detail: "signal: killed: bind failed"}}}
	j.observeHealth(ctx, bound)
	e := expectEvent(t, got, eventbus.EventCoreCrash, "daemon inbound 3")
	if data, ok := e.Data.(*eventbus.CoreCrashData); !ok || data.CoreID != "daemon" || data.InstanceID != 3 || data.Err != "signal: killed: bind failed" {
		t.Fatalf("crash payload = %+v; want the core, the inbound and the daemon's last words", e.Data)
	}
	j.observeHealth(ctx, bound)
	expectQuiet(t, got)

	c.health = core.Health{Instances: []core.InstanceHealth{up}}
	j.observeHealth(ctx, bound)
	expectEvent(t, got, eventbus.EventCoreUp, "daemon inbound 3")
	j.observeHealth(ctx, bound)
	expectQuiet(t, got)

	// systemd: the unit never shows failed; it comes back with Restarts grown.
	restarted := up
	restarted.Restarts = 1
	c.health = core.Health{Instances: []core.InstanceHealth{restarted}}
	j.observeHealth(ctx, bound)
	e = expectEvent(t, got, eventbus.EventCoreCrash, "daemon inbound 3")
	if data := e.Data.(*eventbus.CoreCrashData); data.Err != "restarted by the supervisor (1 times so far)" {
		t.Fatalf("crash detail = %q; want the restart count when the daemon left no words", data.Err)
	}
	// It is up already, so the next pass is its all-clear — not a second crash.
	j.observeHealth(ctx, bound)
	expectEvent(t, got, eventbus.EventCoreUp, "daemon inbound 3")

	// A single-process core reports at the top level and is named by its id.
	single := &healthCore{recordingCore: recordingCore{id: "single", kind: "singlekind"}}
	single.health = core.Health{State: core.HealthRunning, Ready: true}
	sb := core.Bind(single)
	j.observeHealth(ctx, sb)
	single.health = core.Health{State: core.HealthFailed, Detail: "exit status 2"}
	j.observeHealth(ctx, sb)
	expectEvent(t, got, eventbus.EventCoreCrash, "single")
}
