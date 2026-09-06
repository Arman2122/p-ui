package service

import "testing"

/*
A verdict for every client the pass saw, an empty one included.

EvaluateIPLimits `continue`d without a verdict for a client with no cap and for
a client with no inbound, so the caller never learned those clients had been
evaluated: the re-ban memory of the day kept their pairs for the life of the
process (F307), and a caller keying anything on "was this client decided" would
miss them the same way. An empty verdict is the record that there was nothing
to ban and nothing to bounce.
*/
func TestAClientNoLongerCappedStillGetsAVerdict(t *testing.T) {
	initTestDB(t)
	seedPolicyInbound(t, "vless", "vless-uncapped", 45502, "uncapped")
	// No setLimitIP: the client is hosted and uncapped.

	verdicts, err := (&PolicyService{}).EvaluateIPLimits(scanOf("uncapped", "198.51.100.7"))
	if err != nil {
		t.Fatalf("EvaluateIPLimits: %v", err)
	}
	verdict := verdictFor(t, verdicts, "uncapped")
	if len(verdict.Over) != 0 || len(verdict.Cuts) != 0 || len(verdict.Ban) != 0 {
		t.Fatalf("verdict for an uncapped client = %+v, want empty: it exists so the memory forgets, not so anything acts", verdict)
	}
}

func TestADeletedClientStillGetsAVerdict(t *testing.T) {
	initTestDB(t)
	// "ghost" is hosted by no inbound: the orphaned-email branch.

	verdicts, err := (&PolicyService{}).EvaluateIPLimits(scanOf("ghost", "198.51.100.8"))
	if err != nil {
		t.Fatalf("EvaluateIPLimits: %v", err)
	}
	verdict := verdictFor(t, verdicts, "ghost")
	if len(verdict.Over) != 0 || len(verdict.Cuts) != 0 || len(verdict.Ban) != 0 {
		t.Fatalf("verdict for a deleted client = %+v, want empty", verdict)
	}
}
