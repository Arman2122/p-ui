package service

import "testing"

/*
The re-ban memory forgets a client only through a verdict that names it.

reportOverLimit runs AdvancedSince once per verdict, and AdvancedSince is what
drops an email's remembered (email|ip) pairs. EvaluateIPLimits `continue`d
without a verdict for a client with no cap and for a client with no inbound, so
a client whose limit was removed, and a client who was deleted, stayed in the
job's memory for the life of the process. F307.

An empty verdict is the fix: nothing to ban, nothing to bounce, and the caller
now sees the email and lets its pairs go.
*/
func TestAClientNoLongerCappedStillGetsAVerdict(t *testing.T) {
	initTestDB(t)
	seedPolicyInbound(t, "vless", "vless-uncapped", 45502, "uncapped")
	// No setLimitIP: the client is hosted and uncapped.

	verdicts, err := (&PolicyService{}).EvaluateIPLimits(scanOf("uncapped", "198.51.100.7"), true)
	if err != nil {
		t.Fatalf("EvaluateIPLimits: %v", err)
	}
	verdict := verdictFor(t, verdicts, "uncapped")
	if len(verdict.Over) != 0 || verdict.Bounce || verdict.Inbound != nil {
		t.Fatalf("verdict for an uncapped client = %+v, want empty: it exists so the memory forgets, not so anything acts", verdict)
	}
}

func TestADeletedClientStillGetsAVerdict(t *testing.T) {
	initTestDB(t)
	// "ghost" is hosted by no inbound: the orphaned-email branch.

	verdicts, err := (&PolicyService{}).EvaluateIPLimits(scanOf("ghost", "198.51.100.8"), true)
	if err != nil {
		t.Fatalf("EvaluateIPLimits: %v", err)
	}
	verdict := verdictFor(t, verdicts, "ghost")
	if len(verdict.Over) != 0 || verdict.Bounce {
		t.Fatalf("verdict for a deleted client = %+v, want empty", verdict)
	}
}
