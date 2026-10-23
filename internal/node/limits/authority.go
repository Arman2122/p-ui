package limits

import (
	"errors"
	"math"
	"time"
)

func AuthorityAdvance(before, after Policy) bool {
	if before.Revision != after.Revision || before.Term != after.Term || before.ExpiresUnixMilli != after.ExpiresUnixMilli || before.QuotaUnlimited != after.QuotaUnlimited || after.AuthorityRevision <= before.AuthorityRevision || !before.Enabled && after.Enabled || after.CreditRevision < before.CreditRevision || after.CreditCeiling < before.CreditCeiling || after.CreditRevision == before.CreditRevision && after.CreditCeiling != before.CreditCeiling {
		return false
	}
	before.AuthorityRevision, before.Enabled = after.AuthorityRevision, after.Enabled
	before.Upload, before.Download = after.Upload, after.Download
	before.CreditRevision, before.CreditCeiling = after.CreditRevision, after.CreditCeiling
	return before == after
}

func MergeAuthority(current, desired Policy) (Policy, error) {
	if desired.AuthorityRevision == 0 || desired.AuthorityRevision > math.MaxInt64 || desired.CreditRevision == 0 || desired.CreditRevision > math.MaxInt64 || desired.CreditCeiling > math.MaxInt64 {
		return Policy{}, ErrRevision
	}
	if err := validate(desired); err != nil {
		return Policy{}, err
	}
	if desired.Term != current.Term {
		return Policy{}, ErrTerm
	}
	if desired.Revision != current.Revision || desired.ExpiresUnixMilli != current.ExpiresUnixMilli || desired.QuotaUnlimited != current.QuotaUnlimited || !current.Enabled && desired.Enabled {
		return Policy{}, ErrRevision
	}
	if desired.CreditRevision < current.CreditRevision {
		if desired.CreditCeiling > current.CreditCeiling {
			return Policy{}, ErrRevision
		}
		desired.CreditRevision, desired.CreditCeiling = current.CreditRevision, current.CreditCeiling
	}
	if desired == current || AuthorityAdvance(current, desired) {
		return desired, nil
	}
	return Policy{}, ErrRevision
}

func (a *Account) ValidateAuthority(desired Policy) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.quarantined {
		return ErrBlocked
	}
	merged, err := MergeAuthority(a.policy, desired)
	if err != nil {
		return err
	}
	return a.authorityTime(merged)
}

func (a *Account) authorityTime(policy Policy) error {
	if !policy.Enabled {
		return nil
	}
	now := time.Now()
	if a.clock != nil {
		var err error
		now, err = a.clock.Now()
		if err != nil {
			return errors.Join(ErrClock, err)
		}
	}
	if policy.ExpiresUnixMilli != 0 && now.UnixMilli() >= policy.ExpiresUnixMilli {
		return ErrExpired
	}
	return nil
}

func (a *Account) ApplyAuthority(desired Policy) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.quarantined {
		return ErrBlocked
	}
	merged, err := MergeAuthority(a.policy, desired)
	if err != nil {
		return err
	}
	if err := a.authorityTime(merged); err != nil {
		return err
	}
	return a.applyLocked(merged)
}
