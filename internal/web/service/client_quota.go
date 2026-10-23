package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"

	"gorm.io/gorm"
)

var ErrCoreQuotaReset = errors.New("daemon quota reset failed")

var nativeRenewalMu sync.Mutex

type clientRenewal struct {
	email                    string
	from, to                 int64
	previousCount, nextCount int64
	reset                    int
}

var errRenewalSuperseded = errors.New("client renewal superseded by a concurrent lifecycle change")

func nextRenewalCount(current *int64, periods int64) (int64, error) {
	count := int64(0)
	if current != nil {
		count = *current
	}
	if count < 0 || periods < 0 || periods > math.MaxInt64-count {
		return 0, errors.New("client renewal count overflow")
	}
	return count + periods, nil
}

func nativeQuotaClients(tx *gorm.DB) *gorm.DB {
	var kinds []core.Kind
	if reg := registry(); reg != nil {
		for _, bound := range reg.Cores() {
			if bound.Quota != nil {
				kinds = append(kinds, bound.Core.Kinds()...)
			}
		}
	}
	return tx.Table("client_inbounds native_ci").Select("native_c.email").
		Joins("JOIN clients native_c ON native_c.id = native_ci.client_id").
		Joins("JOIN inbounds native_i ON native_i.id = native_ci.inbound_id").
		Where("native_i.node_id IS NULL AND native_i.protocol IN ?", kinds)
}

func dueNativeRenewals(now time.Time) ([]string, error) {
	db := database.GetDB()
	var emails []string
	err := db.Model(&core.ClientTraffic{}).
		Where(legacyTrafficOwnerSQL).
		Where("reset > 0 AND expiry_time > 0 AND expiry_time <= ?", now.UnixMilli()).
		Where(ResellerClientEnabledSQL(), now.UnixMilli()).
		Where("email IN (?)", nativeQuotaClients(db)).Pluck("email", &emails).Error
	return emails, err
}

// The old expiry remains due until the daemon accepts the reset; the next poll retries it.
// Both service calls run outside the traffic writer and retain its ordinary Runtime dispatch.
func (s *InboundService) renewNativeClients(ctx context.Context, emails []string) (bool, error) {
	nativeRenewalMu.Lock()
	defer nativeRenewalMu.Unlock()
	var failures []error
	needRestart := false
	for _, email := range emails {
		if err := ctx.Err(); err != nil {
			return needRestart, errors.Join(append(failures, err)...)
		}
		rec, err := s.clientService.GetRecordByEmail(nil, email)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		now := time.Now().UnixMilli()
		if rec.Reset <= 0 || rec.ExpiryTime <= 0 || rec.ExpiryTime > now {
			continue
		}
		period := int64(rec.Reset) * 86400000
		periods := (now-rec.ExpiryTime)/period + 1
		traffic, err := s.GetClientTrafficByEmail(email)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if traffic == nil {
			continue
		}
		nextCount, err := nextRenewalCount(traffic.ResetCount, periods)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		nr, err := s.clientService.ResetTrafficByEmail(s, email)
		needRestart = needRestart || nr
		if err != nil {
			failures = append(failures, err)
			continue
		}
		current, err := s.clientService.GetRecordByEmail(nil, email)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if current.ExpiryTime != rec.ExpiryTime || current.Reset != rec.Reset {
			continue
		}
		updated := current.ToClient()
		updated.Enable = true
		updated.ExpiryTime += periods * period
		if err := hydrateClientCredentials(nil, rec.Id, 0, updated); err != nil {
			failures = append(failures, err)
			continue
		}
		renewing := *s
		renewing.renewal = &clientRenewal{
			email: email, from: rec.ExpiryTime, to: updated.ExpiryTime,
			previousCount: nextCount - periods, nextCount: nextCount, reset: rec.Reset,
		}
		nr, err = s.clientService.Update(&renewing, rec.Id, *updated)
		needRestart = needRestart || nr
		if !errors.Is(err, errRenewalSuperseded) {
			failures = append(failures, err)
		}
	}
	return needRestart, errors.Join(failures...)
}

// Reset after the client is restored to its core and the traffic writer releases.
// A missing daemon-side client is a no-op under the QuotaEnforcer contract.
func resetCoreQuotas(ctx context.Context, emails []string) error {
	reg := registry()
	if reg == nil || len(emails) == 0 {
		return nil
	}
	var failures []error
	for _, bound := range reg.Cores() {
		failures = append(failures, resetQuotasOn(ctx, bound, emails))
	}
	return errors.Join(failures...)
}

// resetCoreQuotasForAll follows a panel-wide reset: each quota-enforcing core
// is asked to clear the counter of every client it is serving.
func resetCoreQuotasForAll(ctx context.Context) error {
	reg := registry()
	if reg == nil {
		return nil
	}
	svc := &InboundService{}
	var failures []error
	for _, bound := range reg.Cores() {
		if bound.Quota == nil {
			continue
		}
		instances, err := svc.DesiredInstances(bound.Core.Kinds())
		if err != nil {
			failures = append(failures, fmt.Errorf("%w: read core %s clients: %w", ErrCoreQuotaReset, bound.Core.Describe().ID, err))
			continue
		}
		var emails []string
		for _, inst := range instances {
			for _, user := range inst.Users {
				emails = append(emails, user.Email)
			}
		}
		failures = append(failures, resetQuotasOn(ctx, bound, emails))
	}
	return errors.Join(failures...)
}

func resetQuotasOn(ctx context.Context, bound *core.Bound, emails []string) error {
	if bound.Quota == nil {
		return nil
	}
	emails = slices.Clone(emails)
	slices.Sort(emails)
	emails = slices.Compact(emails)
	var failures []error
	for _, email := range emails {
		if email == "" {
			continue
		}
		if err := bound.Quota.ResetQuota(ctx, email); err != nil {
			failures = append(failures, fmt.Errorf("%w: client %q on core %s: %w", ErrCoreQuotaReset, email, bound.Core.Describe().ID, err))
		}
	}
	return errors.Join(failures...)
}
