package service

import (
	"context"
	"slices"
	"testing"
	"time"
)

/*
A client the cap does not apply to keeps its recent addresses too.

EvaluateIPLimits has two branches. The enforcing one decodes what is stored,
merges this pass's observations, and writes back Keep + Retain — everything
still inside the 30-minute stale window. The other branch, taken for a client
with no cap or with no enabled inbound, replaced the whole blob with just what
this poll happened to catch.

So the address list an operator reads for an uncapped client showed only the
connections alive in the last few seconds, while the capped client next to it
showed half an hour. Nothing said the two lists meant different things.

Only display is affected — that branch never reaches a verdict, never bans and
never bounces — but the list is the one place the panel reports where a client
connects from. F305.
*/
func TestAnUncappedClientKeepsItsRecentAddresses(t *testing.T) {
	initTestDB(t)
	svc := &PolicyService{}

	seen := &declaredCore{id: "proxy", kind: "vless"}
	installCores(t, newProxyCore(seen))
	seedPolicyInbound(t, "vless", "vless-uncapped", 45611, "uncapped")
	// No setLimitIP: limit_ip stays 0, which is the non-enforcing branch.

	// First poll: one address, a few minutes ago but well inside the window.
	seen.sessions = observe("uncapped", time.Now().Add(-5*time.Minute), "198.51.100.7")
	if _, err := svc.EvaluateIPLimits(svc.ObserveSessions(context.Background()), false); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	if got := storedAddresses(t, "uncapped"); !slices.Contains(got, "198.51.100.7") {
		t.Fatalf("first pass stored %v, want the address it saw", got)
	}

	// Second poll a moment later, from a different address. The first is still
	// inside the stale window and must not be dropped.
	seen.sessions = observe("uncapped", time.Now(), "198.51.100.8")
	if _, err := svc.EvaluateIPLimits(svc.ObserveSessions(context.Background()), false); err != nil {
		t.Fatalf("second pass: %v", err)
	}

	got := storedAddresses(t, "uncapped")
	if !slices.Contains(got, "198.51.100.8") {
		t.Fatalf("stored %v, missing the address this poll saw", got)
	}
	if !slices.Contains(got, "198.51.100.7") {
		t.Fatalf("stored %v: the address from five minutes ago was dropped, while a capped client would still be showing it", got)
	}
}

// And an address older than the window still ages out, or the list grows
// forever and stops meaning "recently seen".
func TestAnUncappedClientStillForgetsAStaleAddress(t *testing.T) {
	initTestDB(t)
	svc := &PolicyService{}

	seen := &declaredCore{id: "proxy", kind: "vless"}
	installCores(t, newProxyCore(seen))
	seedPolicyInbound(t, "vless", "vless-aging", 45612, "aging")

	seen.sessions = observe("aging", time.Now().Add(-90*time.Minute), "198.51.100.9")
	if _, err := svc.EvaluateIPLimits(svc.ObserveSessions(context.Background()), false); err != nil {
		t.Fatalf("first pass: %v", err)
	}

	seen.sessions = observe("aging", time.Now(), "198.51.100.10")
	if _, err := svc.EvaluateIPLimits(svc.ObserveSessions(context.Background()), false); err != nil {
		t.Fatalf("second pass: %v", err)
	}

	if got := storedAddresses(t, "aging"); slices.Contains(got, "198.51.100.9") {
		t.Fatalf("stored %v: an address seen 90 minutes ago is still on the list", got)
	}
}
