package policy

import "time"

// Calendar months clamp to the last valid day instead of Go's overflow into the next month.
func NextReset(last time.Time, cycle string) time.Time {
	last = last.UTC()
	if cycle == "weekly" {
		return last.AddDate(0, 0, 7)
	}
	if cycle != "monthly" {
		return time.Time{}
	}
	year, month, day := last.Date()
	first := time.Date(year, month+1, 1, last.Hour(), last.Minute(), last.Second(), last.Nanosecond(), time.UTC)
	end := first.AddDate(0, 1, -1).Day()
	return first.AddDate(0, 0, min(day, end)-1)
}

// DepletionLadder retains every tier below the quota and caps both directions afterwards.
func DepletionLadder(p Plan, quota int64) Plan {
	if quota <= 0 {
		return p
	}
	out := Plan{Tiers: make([]Tier, 0, len(p.Tiers)+1)}
	for _, tier := range p.Tiers {
		if tier.FromBytes < quota {
			out.Tiers = append(out.Tiers, tier)
		}
	}
	limits := Evaluate(p, quota)
	capRate := func(rate int64) int64 {
		if rate == 0 {
			return 8 * 1024 * 1024
		}
		return min(rate, 8*1024*1024)
	}
	out.Tiers = append(out.Tiers, Tier{FromBytes: quota, UpBps: capRate(limits.UpBps), DownBps: capRate(limits.DownBps)})
	return out
}
