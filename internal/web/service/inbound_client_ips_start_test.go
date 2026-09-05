package service

import (
	"reflect"
	"testing"
)

// A node's recorded start for a live session must survive the master's push-back,
// or every merge would restart the run and the cap would decide from a tie.
func TestAMergeKeepsTheEarliestRecordedStart(t *testing.T) {
	const now = int64(1_700_000_000)
	old := []clientIpEntry{{IP: "1.1.1.1", Timestamp: now - 100, FirstSeen: now - 600}}
	incoming := []clientIpEntry{
		{IP: "1.1.1.1", Timestamp: now - 10, FirstSeen: now - 300},
		{IP: "2.2.2.2", Timestamp: now - 5, FirstSeen: now - 50},
		{IP: "3.3.3.3", Timestamp: now - 20},
	}
	got := mergeClientIpEntries(old, incoming, now-clientIpStaleAfterSeconds)
	want := []clientIpEntry{
		{IP: "2.2.2.2", Timestamp: now - 5, FirstSeen: now - 50},
		{IP: "1.1.1.1", Timestamp: now - 10, FirstSeen: now - 600},
		{IP: "3.3.3.3", Timestamp: now - 20},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("merged = %+v, want %+v", got, want)
	}

	// A run nobody here had recorded takes the start the other side knows.
	got = mergeClientIpEntries(
		[]clientIpEntry{{IP: "1.1.1.1", Timestamp: now - 100}},
		[]clientIpEntry{{IP: "1.1.1.1", Timestamp: now - 10, FirstSeen: now - 30}}, 0)
	if want := []clientIpEntry{{IP: "1.1.1.1", Timestamp: now - 10, FirstSeen: now - 30}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("merged = %+v, want %+v", got, want)
	}
}
