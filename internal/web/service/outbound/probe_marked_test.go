package outbound

import (
	"testing"
	"time"
)

/*
A working exit must not be called dead because one destination was unreachable.

Measured on the test box: a Surfshark uplink that was up reported a 10s timeout,
because the single address DNS returned for the probe's one destination could
not be reached through it. Three seconds later the same uplink answered in
500ms. The operator's symptom is a red row for a tunnel that is carrying
traffic, and the natural response is to debug the tunnel.
*/
func TestASecondDestinationRescuesAWorkingExit(t *testing.T) {
	var asked []string
	probe := func(_ probeRoute, target string, _ time.Duration, _ bool, out *TestOutboundResult) {
		asked = append(asked, target)
		if target == "first" {
			out.Error = "dial tcp 104.16.132.229:443: i/o timeout"
			return
		}
		out.Success = true
		out.Delay = 503
	}

	result := TestOutboundResult{Tag: "US-sfo | Surfshark"}
	probeMarkedTargets(probe, []string{"first", "second"}, 0x0e000012, "tcp4", "real", &result)

	if !result.Success {
		t.Fatalf("the exit was reported dead though the second destination answered: %+v", result)
	}
	if result.Delay != 503 {
		t.Errorf("delay = %d, want the successful attempt's 503", result.Delay)
	}
	if len(asked) != 2 {
		t.Fatalf("destinations tried = %v, want both", asked)
	}
	// The row's own name has to survive, or the operator cannot tell which exit
	// answered.
	if result.Tag != "US-sfo | Surfshark" {
		t.Errorf("Tag = %q, want the row's name", result.Tag)
	}
}

// An exit that is genuinely unreachable must still be reported as such, with the
// error the last attempt produced rather than a cheerful success.
func TestAnExitThatFailsEverywhereIsStillReportedDown(t *testing.T) {
	probe := func(_ probeRoute, target string, _ time.Duration, _ bool, out *TestOutboundResult) {
		out.Error = "unreachable via " + target
	}

	var result TestOutboundResult
	probeMarkedTargets(probe, []string{"first", "second"}, 0x0e000012, "tcp4", "real", &result)

	if result.Success {
		t.Fatal("every destination failed and the exit was reported working")
	}
	if result.Error != "unreachable via second" {
		t.Errorf("Error = %q, want the last attempt's", result.Error)
	}
}

// The first destination answering must not cost a second request: an exit probe
// sends real traffic through somebody's tunnel.
func TestAWorkingExitIsProbedOnce(t *testing.T) {
	var calls int
	probe := func(_ probeRoute, _ string, _ time.Duration, _ bool, out *TestOutboundResult) {
		calls++
		out.Success = true
	}

	var result TestOutboundResult
	probeMarkedTargets(probe, []string{"first", "second"}, 0x0e000012, "tcp4", "http", &result)

	if calls != 1 {
		t.Fatalf("probed %d times, want 1", calls)
	}
	if result.Mode != "http" {
		t.Errorf("Mode = %q, want http", result.Mode)
	}
}
