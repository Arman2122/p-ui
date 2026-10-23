package native

import (
	"errors"
	"net/netip"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

var (
	ErrAuthority = errors.New("invalid native forwarding authority")
	ErrBusy      = errors.New("native grant has an unsettled kernel slice")
	ErrOwnership = errors.New("native kernel resource is not owned by this installation")
	ErrMissing   = errors.New("native kernel slice is missing")
)

const (
	MaxSliceBytes = 8 << 20
	MaxLeaseTime  = 24 * time.Hour
	MaxClasses    = 60000
	MaxSelectors  = 256
)

var deviceName = regexp.MustCompile(`^phv[a-z0-9]{1,12}$`)

func sliceName(id string) string { return "s" + strings.ReplaceAll(id, "-", "") }

type Selector struct {
	Device  string
	Address netip.Addr
}

type Lease struct {
	ID               string
	Grant            governor.Grant
	Class            uint16
	Bytes            uint64
	ExpiresUnixMilli int64
	Selectors        []Selector
}

func validID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed != uuid.Nil && parsed.String() == id
}

func validateSelectors(selectors []Selector) error {
	if len(selectors) == 0 {
		return ErrAuthority
	}
	return validateSelectorList(selectors, MaxSelectors)
}

func validateSelectorList(selectors []Selector, limit int) error {
	if len(selectors) > limit {
		return ErrAuthority
	}
	seen := make(map[Selector]bool, len(selectors))
	for _, selector := range selectors {
		ip := selector.Address
		if !deviceName.MatchString(selector.Device) || !ip.IsValid() || ip.Zone() != "" || ip.Is4In6() || !ip.IsGlobalUnicast() || seen[selector] {
			return ErrAuthority
		}
		seen[selector] = true
	}
	return nil
}

func (l Lease) validate(now time.Time) error {
	if !validID(l.ID) || governor.Validate(l.Grant) != nil || l.Class == 0 || l.Class > MaxClasses ||
		l.Bytes == 0 || l.Bytes > MaxSliceBytes || l.ExpiresUnixMilli <= now.UnixMilli() ||
		l.ExpiresUnixMilli > now.Add(MaxLeaseTime).UnixMilli() || !l.Grant.Policy.Enabled {
		return ErrAuthority
	}
	policy := l.Grant.Policy
	if policy.ExpiresUnixMilli != 0 && l.ExpiresUnixMilli > policy.ExpiresUnixMilli ||
		!policy.QuotaUnlimited && l.Bytes > policy.CreditCeiling {
		return ErrAuthority
	}
	for _, rate := range []limits.Rate{policy.Upload, policy.Download} {
		if rate.Mode == limits.Limited && (rate.BitsPerSecond < 8 || rate.BurstBytes < 65536) {
			return ErrAuthority
		}
	}
	return validateSelectors(l.Selectors)
}

type Counters struct {
	Uploaded   uint64
	Downloaded uint64
}
