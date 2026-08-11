package policy

// Tier is one rung of a rate ladder; it binds once usage reaches FromBytes.
// UpBps and DownBps are bits per second from the CLIENT's side; 0 is unlimited.
type Tier struct {
	FromBytes      int64
	UpBps, DownBps int64
}

// Plan is one client's whole ladder. A plain speed limit is a one-tier plan
// starting at 0, so thresholds and speed limits cannot drift into two features.
type Plan struct{ Tiers []Tier }

// Limits is what a client may push right now, per direction. The zero value is
// unlimited, never blocked — that is what an absent or malformed plan yields.
type Limits struct{ UpBps, DownBps int64 }

// Evaluate picks the highest tier whose FromBytes <= usedBytes; order-insensitive,
// so an unsorted ladder answers alike. No match is unlimited: never throttle on junk.
func Evaluate(p Plan, usedBytes int64) Limits {
	var (
		best  Tier
		found bool
	)
	for _, tier := range p.Tiers {
		if tier.FromBytes > usedBytes {
			continue
		}
		// >= so the LAST of two equal FromBytes wins, which is what "the last
		// matching tier" means once the writer has sorted the ladder.
		if !found || tier.FromBytes >= best.FromBytes {
			best, found = tier, true
		}
	}
	if !found {
		return Limits{}
	}
	return Limits{UpBps: best.UpBps, DownBps: best.DownBps}
}
