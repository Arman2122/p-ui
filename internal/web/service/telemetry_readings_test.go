package service

import (
	"testing"
	"time"
)

func TestHostHistoryOmitsUnavailableReadings(t *testing.T) {
	previous := systemMetrics
	systemMetrics = newMetricHistory()
	t.Cleanup(func() { systemMetrics = previous })
	status := &Status{Cpu: 20, Unavailable: []string{"cpu", "netUp", "tcpCount"}}
	svc := &ServerService{}
	svc.AppendStatusSample(time.Now(), status)
	window := svc.SystemHistoryWindow(60)
	for _, key := range []string{"cpu", "netUp", "tcpCount"} {
		if len(window.Series[key]) != 0 {
			t.Fatalf("unavailable %s produced readings: %+v", key, window.Series[key])
		}
	}
	if points := window.Series["netDown"]; len(points) != 1 || points[0].V != 0 {
		t.Fatalf("an observed zero rate must be retained: %+v", points)
	}
}

func TestHostInterfaceRateAfterCounterReset(t *testing.T) {
	for _, tc := range []struct {
		name              string
		current, previous uint64
		seconds           float64
		want              uint64
	}{
		{"steady", 1600, 1000, 2, 300},
		{"interface removed", 100, 1000, 2, 0},
		{"same timestamp", 1600, 1000, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := interfaceRate(tc.current, tc.previous, tc.seconds); got != tc.want {
				t.Fatalf("rate=%d, want %d", got, tc.want)
			}
		})
	}
}
