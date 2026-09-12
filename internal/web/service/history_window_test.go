package service

import (
	"testing"
	"time"
)

func TestFifteenMinuteHistoryUsesSixtyBuckets(t *testing.T) {
	history := newMetricHistory()
	end := time.Now().Unix() / 15 * 15
	for i := range 61 {
		start := end - int64(60-i)*15
		history.append("cpu", time.Unix(start+2, 0), 10)
		history.append("cpu", time.Unix(start+4, 0), 30)
	}
	points := history.aggregate("cpu", 15, 60)
	if len(points) != 60 {
		t.Fatalf("got %d buckets, want 60", len(points))
	}
	for i, point := range points {
		wantTime := end - int64(59-i)*15
		if point["t"] != wantTime || point["v"] != float64(20) {
			t.Fatalf("bucket %d = %v, want t=%d v=20", i, point, wantTime)
		}
	}
	if span := points[59]["t"].(int64) - points[0]["t"].(int64) + 15; span != 15*60 {
		t.Fatalf("window spans %ds, want 900s", span)
	}
}
