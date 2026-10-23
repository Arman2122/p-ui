package limits

import (
	"errors"
	"math"
	"time"
)

func CreditAdvance(before, after Policy) bool {
	if before.QuotaUnlimited || after.CreditRevision <= before.CreditRevision || after.CreditCeiling <= before.CreditCeiling {
		return false
	}
	before.CreditRevision, before.CreditCeiling = after.CreditRevision, after.CreditCeiling
	return before == after
}

func RetainCredit(base, persisted Policy) Policy {
	if base.Revision != persisted.Revision || base.CreditRevision >= persisted.CreditRevision || base.CreditCeiling > persisted.CreditCeiling {
		return base
	}
	merged := base
	merged.CreditRevision, merged.CreditCeiling = persisted.CreditRevision, persisted.CreditCeiling
	if merged == persisted {
		return persisted
	}
	return base
}

func (a *Account) ApplyCredit(term string, policyRevision, creditRevision, ceiling uint64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if term != a.policy.Term {
		return ErrTerm
	}
	if policyRevision != a.policy.Revision || creditRevision == 0 || creditRevision > math.MaxInt64 || ceiling > math.MaxInt64 {
		return ErrRevision
	}
	p := a.policy
	p.CreditRevision, p.CreditCeiling = creditRevision, ceiling
	if p == a.policy {
		return nil
	}
	if !CreditAdvance(a.policy, p) {
		return ErrRevision
	}
	if a.quarantined || !p.Enabled || p.Upload.Mode == Blocked && p.Download.Mode == Blocked {
		return ErrBlocked
	}
	now := time.Now()
	if a.clock != nil {
		var err error
		now, err = a.clock.Now()
		if err != nil {
			return errors.Join(ErrClock, err)
		}
	}
	if p.ExpiresUnixMilli != 0 && now.UnixMilli() >= p.ExpiresUnixMilli {
		return ErrExpired
	}
	return a.applyLocked(p)
}
