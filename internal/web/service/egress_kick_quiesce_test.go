package service

import (
	"testing"
	"time"
)

/*
The harness must not swap the egress manager under a kick that is still
reading it.

kickEgressAfterCoreRestart starts a goroutine that outlives its caller by the
whole delay ladder -- three seconds in production -- and reads egressManager on
every pass. withFakeEgressKernel writes that variable. Under -shuffle a test
that restarted a core was followed by one that installed the fake, and the
race detector reported exactly that pair (CI run 33641933638). Nothing else
was wrong, so it passed on the next commit; a harness race is a red CI at
random, for anyone, forever.

Deterministic under -race: a kick is started here and the harness is asked to
swap immediately. Without the wait the two accesses have no ordering between
them, and the detector reports it whatever the clock says.
*/
func TestEgressHarnessWaitsForTheRestartKick(t *testing.T) {
	initTestDB(t)
	previous := egressRestartKickDelays
	egressRestartKickDelays = []time.Duration{time.Millisecond, time.Millisecond}
	t.Cleanup(func() { egressRestartKickDelays = previous })

	kickEgressAfterCoreRestart()
	withFakeEgressKernel(t)
}
