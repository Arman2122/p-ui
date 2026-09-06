package job

import (
	"context"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/eventbus"
	"github.com/Arman2122/p-ui/internal/policy"
	"github.com/Arman2122/p-ui/internal/web/service"
)

func setSettingRow(t *testing.T, key, value string) {
	t.Helper()
	row := model.Setting{Key: key}
	if err := database.GetDB().Where("key = ?", key).Assign(model.Setting{Value: value}).FirstOrCreate(&row).Error; err != nil {
		t.Fatalf("set %s: %v", key, err)
	}
}

// collectDeviceLimits is collect for the device-limit event, which the crash
// collector leaves on the floor.
func collectDeviceLimits(t *testing.T, bus *eventbus.Bus) <-chan eventbus.Event {
	t.Helper()
	got := make(chan eventbus.Event, 16)
	bus.Subscribe("test-device-limit", func(e eventbus.Event) {
		if e.Type == eventbus.EventClientDeviceLimit {
			got <- e
		}
	})
	return got
}

func deviceLimitData(t *testing.T, e eventbus.Event) *eventbus.ClientDeviceLimitData {
	t.Helper()
	data, ok := e.Data.(*eventbus.ClientDeviceLimitData)
	if !ok {
		t.Fatalf("event %s carries %T, want the device-limit payload", e.Type, e.Data)
	}
	return data
}

/*
A device over its cap is one event per act, never one per pass: a cut that
ended something, a ban the pass created, a shared address it reported instead.
The pass after, with the device still over the cap and its ban still held,
publishes nothing, or the operator's chat fills every ten seconds and the
client is told the same thing all day.
*/
func TestADeviceOverItsCapIsOneEventPerAct(t *testing.T) {
	initTestDB(t)
	t.Setenv("PUI_LOG_FOLDER", t.TempDir())
	installBanPlane(t)
	bus := eventbus.New(16)
	t.Cleanup(bus.Stop)
	EventBus = bus
	t.Cleanup(func() { EventBus = nil })
	got := collectDeviceLimits(t, bus)
	job := NewCorePolicyJob()
	now := time.Now()

	t.Run("a ban, once", func(t *testing.T) {
		over := []service.IPLimitVerdict{{
			Email: "alice", Limit: 1,
			Keep: []policy.Observation{{IP: "10.0.0.1"}}, Over: []policy.Observation{{IP: "10.0.0.9"}},
			Ban: []policy.Observation{{IP: "10.0.0.9"}}, BanPorts: []uint16{443},
		}}
		job.reportOverLimit(context.Background(), over, now)
		e := expectEvent(t, got, eventbus.EventClientDeviceLimit, "alice")
		data := deviceLimitData(t, e)
		if data.Action != "ban" || data.IP != "10.0.0.9" || data.Limit != 1 || data.Devices != 2 || data.Until != now.Unix()+30*60 || data.Where != "" {
			t.Fatalf("payload %+v, want the ban of 10.0.0.9 with 2 devices over a cap of 1 until +30m", data)
		}
		job.reportOverLimit(context.Background(), over, now.Add(10*time.Second))
		expectQuiet(t, got)
	})

	t.Run("a cut", func(t *testing.T) {
		fake := &cutFakeCore{sessionFakeCore: &sessionFakeCore{fakeCore: &fakeCore{id: "tuic"}}}
		installSessionCore(t, fake)
		seedObservedClient(t, "tuic", "tuic-cut", 45781, "bob")
		var inbound model.Inbound
		if err := database.GetDB().Where("tag = ?", "tuic-cut").First(&inbound).Error; err != nil {
			t.Fatalf("inbound: %v", err)
		}
		verdict := []service.IPLimitVerdict{{
			Email: "bob", Limit: 1, Keep: []policy.Observation{{IP: "10.0.0.2"}}, Over: []policy.Observation{{IP: "10.0.0.8"}},
			Cuts: []service.IPLimitCut{{Inbound: &inbound, Source: "10.0.0.8"}},
		}}
		job.reportOverLimit(context.Background(), verdict, now)
		data := deviceLimitData(t, expectEvent(t, got, eventbus.EventClientDeviceLimit, "bob"))
		if data.Action != "cut" || data.IP != "10.0.0.8" {
			t.Fatalf("payload %+v, want the cut of 10.0.0.8", data)
		}
	})

	t.Run("a shared address reported, once", func(t *testing.T) {
		setSettingRow(t, "ipLimitSharedAddressAction", service.SharedAddressSkip)
		shared := []service.IPLimitVerdict{
			{Email: "neighbour", Limit: 5, Keep: []policy.Observation{{IP: "10.0.0.7"}}},
			{
				Email: "carol", Limit: 1, Keep: []policy.Observation{{IP: "10.0.0.3"}}, Over: []policy.Observation{{IP: "10.0.0.7"}},
				Ban: []policy.Observation{{IP: "10.0.0.7"}}, BanPorts: []uint16{443},
			},
		}
		job.reportOverLimit(context.Background(), shared, now)
		data := deviceLimitData(t, expectEvent(t, got, eventbus.EventClientDeviceLimit, "carol"))
		if data.Action != "reported" || data.IP != "10.0.0.7" {
			t.Fatalf("payload %+v, want 10.0.0.7 reported and not banned", data)
		}
		job.reportOverLimit(context.Background(), shared, now.Add(10*time.Second))
		expectQuiet(t, got)
	})
}
