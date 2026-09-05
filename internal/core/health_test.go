package core

import "testing"

// Overall is the one derivation of a per-inbound core's health, so every branch
// is pinned: the page, the event pass and coretest must agree on "degraded".
func TestOverallDerivesOneStateFromTheInstances(t *testing.T) {
	inst := func(state HealthState, ready bool) InstanceHealth {
		return InstanceHealth{State: state, Ready: ready}
	}
	cases := []struct {
		name      string
		health    Health
		wantState HealthState
		wantReady bool
	}{
		{"a single process answers for itself", Health{State: HealthRunning, Ready: true}, HealthRunning, true},
		{"a single process not yet answering", Health{State: HealthRunning}, HealthRunning, false},
		{"a per-inbound core with nothing to run is idle", Health{}, HealthIdle, true},
		{"every instance running and answering", Health{Instances: []InstanceHealth{inst(HealthRunning, true), inst(HealthRunning, true)}}, HealthRunning, true},
		{"every instance running, one not answering", Health{Instances: []InstanceHealth{inst(HealthRunning, true), inst(HealthRunning, false)}}, HealthRunning, false},
		{"one of two instances down", Health{Instances: []InstanceHealth{inst(HealthRunning, true), inst(HealthFailed, false)}}, HealthDegraded, false},
		{"every instance failed", Health{Instances: []InstanceHealth{inst(HealthFailed, false), inst(HealthFailed, false)}}, HealthFailed, false},
		{"every instance stopped", Health{Instances: []InstanceHealth{inst(HealthStopped, false)}}, HealthStopped, false},
		{"stopped beside failed reads as failed", Health{Instances: []InstanceHealth{inst(HealthStopped, false), inst(HealthFailed, false)}}, HealthFailed, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state, ready := tc.health.Overall()
			if state != tc.wantState || ready != tc.wantReady {
				t.Fatalf("Overall() = (%s, %v), want (%s, %v)", state, ready, tc.wantState, tc.wantReady)
			}
		})
	}
}
