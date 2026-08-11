package job

import (
	"reflect"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/policy"
)

// Both implementations over the same inputs, so the lift into internal/policy is
// proven rather than assumed. It dies with check_client_ip_job.go.
func toObservations(entries []IPWithTimestamp) []policy.Observation {
	out := make([]policy.Observation, 0, len(entries))
	for _, e := range entries {
		out = append(out, policy.Observation{IP: e.IP, LastSeenUnixMilli: e.Timestamp * 1000})
	}
	return out
}

func toEntries(observations []policy.Observation) []IPWithTimestamp {
	out := make([]IPWithTimestamp, 0, len(observations))
	for _, o := range observations {
		out = append(out, IPWithTimestamp{IP: o.IP, Timestamp: o.LastSeenUnixMilli / 1000})
	}
	return out
}

func assertSameEntries(t *testing.T, label string, got, want []IPWithTimestamp) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: internal/policy gave %v, the job gave %v", label, got, want)
	}
	if len(want) > 0 && !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: internal/policy gave %v, the job gave %v", label, got, want)
	}
}

func TestPolicyDecideMatchesTheJobItReplaces(t *testing.T) {
	// Every timestamp sits far from the 120s and 30m boundaries and none repeat,
	// so partitionLiveIps reading its own clock cannot flip a row.
	now := time.Now().Unix()
	staleCutoff := now - ipStaleAfterSeconds

	tests := []struct {
		name            string
		persisted       []IPWithTimestamp
		observed        []IPWithTimestamp
		limit           int
		observedAreLive bool
	}{
		{
			name:            "three live addresses over a cap of two",
			observed:        []IPWithTimestamp{{IP: "10.0.0.1", Timestamp: now - 30}, {IP: "10.0.0.2", Timestamp: now - 20}, {IP: "10.0.0.3", Timestamp: now - 10}},
			limit:           2,
			observedAreLive: true,
		},
		{
			name:            "an unlimited cap keeps every address",
			observed:        []IPWithTimestamp{{IP: "10.0.0.1", Timestamp: now - 30}, {IP: "10.0.0.2", Timestamp: now - 20}, {IP: "10.0.0.3", Timestamp: now - 10}},
			limit:           0,
			observedAreLive: true,
		},
		{
			name:            "a persisted entry past the stale cutoff is dropped",
			persisted:       []IPWithTimestamp{{IP: "10.0.0.9", Timestamp: now - 3600}},
			observed:        []IPWithTimestamp{{IP: "10.0.0.1", Timestamp: now - 10}},
			limit:           2,
			observedAreLive: true,
		},
		{
			name:            "a persisted entry inside the cross-node window counts live",
			persisted:       []IPWithTimestamp{{IP: "10.0.0.5", Timestamp: now - 30}},
			observed:        []IPWithTimestamp{{IP: "10.0.0.1", Timestamp: now - 10}},
			limit:           1,
			observedAreLive: true,
		},
		{
			name:            "a persisted entry outside the window is historical",
			persisted:       []IPWithTimestamp{{IP: "10.0.0.5", Timestamp: now - 600}},
			observed:        []IPWithTimestamp{{IP: "10.0.0.1", Timestamp: now - 10}},
			limit:           1,
			observedAreLive: true,
		},
		{
			name:            "an hours-old observation survives because it is live",
			observed:        []IPWithTimestamp{{IP: "10.0.0.1", Timestamp: now - 7200}},
			limit:           2,
			observedAreLive: true,
		},
		{
			name:            "the same observation is dropped without the live flag",
			observed:        []IPWithTimestamp{{IP: "10.0.0.1", Timestamp: now - 7200}},
			limit:           2,
			observedAreLive: false,
		},
		{
			name:            "the later timestamp wins when both sides know an address",
			persisted:       []IPWithTimestamp{{IP: "10.0.0.1", Timestamp: now - 600}},
			observed:        []IPWithTimestamp{{IP: "10.0.0.1", Timestamp: now - 10}},
			limit:           1,
			observedAreLive: true,
		},
		{
			name:            "a quiet pass leaves every persisted entry historical",
			persisted:       []IPWithTimestamp{{IP: "10.0.0.1", Timestamp: now - 600}, {IP: "10.0.0.2", Timestamp: now - 700}},
			limit:           1,
			observedAreLive: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			merged := mergeClientIps(tt.persisted, tt.observed, staleCutoff, tt.observedAreLive)
			observedThisScan := make(map[string]bool, len(tt.observed))
			for _, e := range tt.observed {
				observedThisScan[e.IP] = true
			}
			live, historical := partitionLiveIps(merged, observedThisScan)
			wantKeep, wantBan := selectIpsToBan(live, tt.limit)

			got := policy.Decide(
				toObservations(tt.persisted),
				toObservations(tt.observed),
				tt.limit,
				now*1000,
				staleCutoff*1000,
				120*time.Second,
				tt.observedAreLive,
			)

			assertSameEntries(t, "Keep", toEntries(got.Keep), wantKeep)
			assertSameEntries(t, "Ban", toEntries(got.Ban), wantBan)
			assertSameEntries(t, "Retain", toEntries(got.Retain), historical)
		})
	}
}

func TestPolicyAdvancedSinceMatchesTheJobItReplaces(t *testing.T) {
	const email = "lift@example.test"
	now := time.Now().Unix()
	first := []IPWithTimestamp{{IP: "10.0.0.1", Timestamp: now}}
	reconnected := []IPWithTimestamp{{IP: "10.0.0.1", Timestamp: now + 60}}

	passes := []struct {
		name   string
		banned []IPWithTimestamp
	}{
		{"first ban", first},
		{"frozen rescan", first},
		{"reconnect", reconnected},
		{"no longer over the cap", nil},
		{"banned again after clearing", first},
	}

	j := &CheckClientIpJob{}
	var seen map[string]int64
	for _, pass := range passes {
		t.Run(pass.name, func(t *testing.T) {
			want := j.filterAdvancedSinceLastBan(email, pass.banned)

			var actionable []policy.Observation
			actionable, seen = policy.AdvancedSince(email, toObservations(pass.banned), seen)

			assertSameEntries(t, "actionable", toEntries(actionable), want)
		})
	}
}
