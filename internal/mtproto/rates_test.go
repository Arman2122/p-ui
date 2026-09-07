package mtproto

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

// The contract speaks bits per second and the sidecar bytes; the floor the
// sidecar refuses below is met by raising, since a refused PUT restarts the
// process on every pass for a plan that merely asked for less than 64 kbit/s.
func TestSidecarRate(t *testing.T) {
	for _, tc := range []struct {
		bits, want int64
	}{
		{0, 0},
		{-8, 0},
		{1_000_000, 125_000},
		{2_000_000, 250_000},
		{64_000, 8_000},
		{32_000, 8_000},
		{7, 8_000},
	} {
		if got := sidecarRate(tc.bits); got != tc.want {
			t.Fatalf("sidecarRate(%d) = %d, want %d", tc.bits, got, tc.want)
		}
	}
}

/*
The ladder is folded into the instance before the fingerprints are taken, so the
supervise pass, a client edit and the rate pass all render the same file and the
same PUT body; and a ladder that did not move is not a change, or the rate pass
would rebuild every client's bucket every ten seconds.
*/
func TestRatesAreFoldedIntoTheInstanceOnce(t *testing.T) {
	m := &Manager{procs: map[int]*managed{}}
	ladder := map[string]core.RateLimit{"alice": {UpBps: 1_000_000, DownBps: 2_000_000}}
	if !m.SetRates(7, ladder) {
		t.Fatal("the first ladder is a change")
	}
	if m.SetRates(7, map[string]core.RateLimit{"alice": {UpBps: 1_000_000, DownBps: 2_000_000}}) {
		t.Fatal("the same ladder again is not a change")
	}

	inst := Instance{Id: 7, Secrets: []SecretEntry{{Name: "alice", Secret: "ee11"}, {Name: "bob", Secret: "ee22"}}}
	got := m.withRatesLocked(inst)
	if a := got.Secrets[0]; a.SpeedUpBytesPerSecond != 125_000 || a.SpeedDownBytesPerSecond != 250_000 {
		t.Fatalf("alice carries %+v, want 125000 up and 250000 down", a)
	}
	if b := got.Secrets[1]; b.SpeedUpBytesPerSecond != 0 || b.SpeedDownBytesPerSecond != 0 {
		t.Fatalf("bob was handed no rate and carries %+v", b)
	}
	if inst.Secrets[0].SpeedUpBytesPerSecond != 0 {
		t.Fatal("the overlay must copy: the caller's instance is the supervise pass's and is rendered again")
	}
	if inst.SecretsFingerprint() == got.SecretsFingerprint() {
		t.Fatal("a rate is part of the secrets fingerprint, or nothing would ever push it")
	}

	if !m.SetRates(7, nil) {
		t.Fatal("clearing a held ladder is a change")
	}
	if m.SetRates(7, nil) {
		t.Fatal("clearing an absent ladder is not")
	}
	if cleared := m.withRatesLocked(inst); cleared.Secrets[0].SpeedUpBytesPerSecond != 0 {
		t.Fatalf("a cleared ladder still reaches the render: %+v", cleared.Secrets[0])
	}
}
