package controlplane

import (
	"errors"
	"fmt"
	"math/bits"
	"slices"
	"strings"

	"github.com/Arman2122/p-ui/internal/node/limits"
)

type RateParticipant struct {
	ID           string
	Outstanding  Rates
	Eligible     bool
	Frozen       bool
	MinimumBurst int
}

type RateAllocation struct {
	ID     string
	Target Rates
	Next   Rates
}

type RateCapacityError struct {
	Direction      limits.Direction
	AvailableBits  uint64
	AvailableBurst int
	Participants   int
	RequiredBurst  uint64
}

func (e *RateCapacityError) Error() string {
	return fmt.Sprintf("shared bandwidth cannot admit %d participants in direction %d within %d bits/second and %d burst bytes (minimum burst %d)", e.Participants, e.Direction, e.AvailableBits, e.AvailableBurst, e.RequiredBurst)
}

// Next retains all outstanding authority when testing an increase; reductions require separate applied proof.
func AllocateSharedRates(budget Rates, participants []RateParticipant) ([]RateAllocation, error) {
	if budget.validate() != nil || len(participants) > 32768 {
		return nil, ErrInvalid
	}
	participants = slices.Clone(participants)
	slices.SortFunc(participants, func(a, b RateParticipant) int { return strings.Compare(a.ID, b.ID) })
	for i, participant := range participants {
		if !validID(participant.ID) || participant.Outstanding.validate() != nil || participant.MinimumBurst < 1 || participant.MinimumBurst > 1<<20 ||
			i > 0 && participants[i-1].ID == participant.ID {
			return nil, ErrInvalid
		}
	}
	result := make([]RateAllocation, len(participants))
	var constraints error
	for i, participant := range participants {
		result[i].ID = participant.ID
	}
	for direction, bound := range []limits.Rate{budget.Upload, budget.Download} {
		if bound.Mode == limits.Limited {
			count, minimum := 0, uint64(0)
			for _, participant := range participants {
				if participant.Eligible && !participant.Frozen {
					count++
					minimum += uint64(participant.MinimumBurst)
				}
			}
			if uint64(count) > bound.BitsPerSecond || minimum > uint64(bound.BurstBytes) {
				constraints = errors.Join(constraints, &RateCapacityError{Direction: limits.Direction(direction), AvailableBits: bound.BitsPerSecond, AvailableBurst: bound.BurstBytes, Participants: count, RequiredBurst: minimum})
			}
		}
		current := make([]limits.Rate, len(participants))
		for i, participant := range participants {
			current[i] = participant.Outstanding.Upload
			if direction == 1 {
				current[i] = participant.Outstanding.Download
			}
		}
		target := fairRateTargets(bound, participants, current)
		var total rateTotals
		for _, rate := range current {
			total.add(rate)
		}
		for i := range participants {
			next := lowerRate(current[i], target[i])
			if participants[i].Frozen {
				next = current[i]
			} else {
				candidate := total
				candidate.remove(current[i])
				candidate.add(envelope(current[i], target[i]))
				if candidate.fits(bound) {
					next = target[i]
				}
			}
			total.remove(current[i])
			current[i] = envelope(current[i], next)
			total.add(current[i])
			if direction == 0 {
				result[i].Target.Upload, result[i].Next.Upload = target[i], next
			} else {
				result[i].Target.Download, result[i].Next.Download = target[i], next
			}
		}
	}
	return result, constraints
}

type rateTotals struct {
	bits, high, burst uint64
	unlimited         int
}

func (s *rateTotals) add(value limits.Rate) {
	switch value.Mode {
	case limits.Unlimited:
		s.unlimited++
	case limits.Limited:
		var carry uint64
		s.bits, carry = bits.Add64(s.bits, value.BitsPerSecond, 0)
		s.high += carry
		s.burst += uint64(value.BurstBytes)
	}
}

func (s *rateTotals) remove(value limits.Rate) {
	switch value.Mode {
	case limits.Unlimited:
		s.unlimited--
	case limits.Limited:
		var borrow uint64
		s.bits, borrow = bits.Sub64(s.bits, value.BitsPerSecond, 0)
		s.high -= borrow
		s.burst -= uint64(value.BurstBytes)
	}
}

func (s rateTotals) fits(bound limits.Rate) bool {
	return bound.Mode == limits.Unlimited || s.unlimited == 0 && s.high == 0 && s.bits <= bound.BitsPerSecond && s.burst <= uint64(bound.BurstBytes)
}

func lowerRate(a, b limits.Rate) limits.Rate {
	if a.Mode == limits.Blocked || b.Mode == limits.Blocked {
		return limits.Rate{}
	}
	if a.Mode == limits.Unlimited {
		return b
	}
	if b.Mode == limits.Unlimited {
		return a
	}
	return limits.Rate{Mode: limits.Limited, BitsPerSecond: min(a.BitsPerSecond, b.BitsPerSecond), BurstBytes: min(a.BurstBytes, b.BurstBytes)}
}

func fairRateTargets(bound limits.Rate, participants []RateParticipant, current []limits.Rate) []limits.Rate {
	result := make([]limits.Rate, len(participants))
	var eligible []int
	bits, burst := bound.BitsPerSecond, uint64(bound.BurstBytes)
	insufficient := bound.Mode == limits.Blocked
	for i, participant := range participants {
		if participant.Frozen {
			result[i] = current[i]
			if bound.Mode != limits.Unlimited && current[i].Mode != limits.Blocked {
				if current[i].Mode == limits.Unlimited || current[i].BitsPerSecond > bits || uint64(current[i].BurstBytes) > burst {
					insufficient = true
				} else {
					bits -= current[i].BitsPerSecond
					burst -= uint64(current[i].BurstBytes)
				}
			}
		} else if participant.Eligible {
			eligible = append(eligible, i)
		}
	}
	if len(eligible) == 0 || insufficient {
		return result
	}
	if bound.Mode == limits.Unlimited {
		for _, i := range eligible {
			result[i] = limits.Rate{Mode: limits.Unlimited}
		}
		return result
	}
	if bits < uint64(len(eligible)) {
		return retainFeasibleRates(bound, participants, current, result)
	}
	var minimum uint64
	for _, i := range eligible {
		minimum += uint64(participants[i].MinimumBurst)
	}
	if minimum > burst {
		return retainFeasibleRates(bound, participants, current, result)
	}
	count := uint64(len(eligible))
	extra := burst - minimum
	for rank, i := range eligible {
		assignedBits, assignedBurst := bits/count, uint64(participants[i].MinimumBurst)+extra/count
		if uint64(rank) < bits%count {
			assignedBits++
		}
		if uint64(rank) < extra%count {
			assignedBurst++
		}
		result[i] = limits.Rate{Mode: limits.Limited, BitsPerSecond: assignedBits, BurstBytes: int(assignedBurst)}
	}
	return result
}

func retainFeasibleRates(bound limits.Rate, participants []RateParticipant, current, result []limits.Rate) []limits.Rate {
	if fitsRates(bound, current) {
		for i, participant := range participants {
			if participant.Eligible && !participant.Frozen {
				result[i] = current[i]
			}
		}
	}
	return result
}
