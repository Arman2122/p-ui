package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/policy"
)

// PlanQuotaDisableSQL belongs inside the quota branch, never around the expiry branch.
func PlanQuotaDisableSQL() string {
	return `NOT EXISTS (SELECT 1 FROM client_policies plan_cp JOIN policies plan_p ON plan_p.id = plan_cp.policy_id WHERE plan_cp.email = client_traffics.email AND NOT plan_cp.override_quota AND plan_cp.applied_on_depletion IN ('throttle','notify'))`
}

type PlanDepletionNotice struct {
	Email       string `json:"email" example:"alice"`
	PolicyID    int    `json:"policyId" example:"1"`
	PlanName    string `json:"planName" example:"Family"`
	OwnerUserID *int   `json:"ownerUserId" example:"2"`
	UsedBytes   int64  `json:"usedBytes" example:"53687091200"`
	QuotaBytes  int64  `json:"quotaBytes" example:"53687091200"`
}

type PlanCycleResult struct {
	Reset       int                   `json:"reset" example:"2"`
	NeedRestart bool                  `json:"needRestart" example:"false"`
	Notices     []PlanDepletionNotice `json:"notices"`
}

// planCycleSubject is one assignment's client as the pass reads it: the record,
// whether its reseller may serve it, and its usage, in one row per client.
type planCycleSubject struct {
	ID           int
	Email        string
	Enable       bool
	ExpiryTime   int64
	TotalGB      int64
	OwnerAllowed bool
	UsedBytes    int64
}

// planCycleSubjects reads every named client in chunks. A client with no traffic
// row is absent, which the caller skips exactly as a per-client read did.
func planCycleSubjects(db *gorm.DB, emails []string, now time.Time) (map[string]planCycleSubject, error) {
	out := make(map[string]planCycleSubject, len(emails))
	used, usedArgs := UsedBytesExpr(db)
	for _, batch := range chunkStrings(emails, sqlInChunk) {
		var rows []planCycleSubject
		args := append(append([]any{}, usedArgs...), now.UnixMilli())
		err := db.Table("clients").
			Select("clients.id, clients.email, clients.enable, clients.expiry_time, clients.total_gb, "+
				used+" AS used_bytes, "+ResellerClientEnabledSQL()+" AS owner_allowed", args...).
			Joins("JOIN client_traffics ON client_traffics.email = clients.email").
			Where(legacyTrafficOwnerSQL).
			Where("clients.email IN ?", batch).
			Scan(&rows).Error
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			out[row.Email] = row
		}
	}
	return out, nil
}

func planCyclePlans(db *gorm.DB, assignments []model.ClientPolicy) (map[int]*model.Policy, error) {
	ids := make([]int, 0, len(assignments))
	for _, a := range assignments {
		if a.PolicyId != nil {
			ids = append(ids, *a.PolicyId)
		}
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	out := make(map[int]*model.Policy, len(ids))
	for _, batch := range chunkInts(ids, sqlInChunk) {
		var rows []*model.Policy
		if err := db.Where("id IN ?", batch).Find(&rows).Error; err != nil {
			return nil, err
		}
		for _, row := range rows {
			out[row.Id] = row
		}
	}
	return out, nil
}

// ConvergePlanCycles runs due resets and depletion notices from a few batched reads under the
// assignment lock; a client reset this pass is judged for depletion next pass, its usage being stale.
func (s *PolicyService) ConvergePlanCycles(ctx context.Context, now time.Time) (PlanCycleResult, error) {
	out := PlanCycleResult{Notices: []PlanDepletionNotice{}}
	planAssignmentMu.Lock()
	defer planAssignmentMu.Unlock()
	db := database.GetDB()
	var assignments []model.ClientPolicy
	if err := db.Joins("JOIN policies p ON p.id=client_policies.policy_id").Where("client_policies.applied_reset_cycle <> 'none' OR client_policies.applied_on_depletion = 'notify'").Find(&assignments).Error; err != nil {
		return out, err
	}
	if len(assignments) == 0 {
		return out, nil
	}
	plans, err := planCyclePlans(db, assignments)
	if err != nil {
		return out, err
	}
	emails := make([]string, 0, len(assignments))
	for _, a := range assignments {
		emails = append(emails, a.Email)
	}
	subjects, err := planCycleSubjects(db, emails, now)
	if err != nil {
		return out, err
	}
	var failures []error
	for _, a := range assignments {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		p := plans[*a.PolicyId]
		if p == nil {
			failures = append(failures, fmt.Errorf("%w: %d", ErrPolicyUnknown, *a.PolicyId))
			continue
		}
		rec, known := subjects[a.Email]
		if !known || !rec.OwnerAllowed {
			continue
		}
		if rec.ExpiryTime < 0 || (rec.ExpiryTime > 0 && rec.ExpiryTime <= now.UnixMilli()) {
			continue
		}
		anchor := a.LastResetAt
		if anchor == 0 {
			anchor = a.UpdatedAt
		}
		if a.AppliedDurationDays != nil && *a.AppliedDurationDays > 0 && rec.ExpiryTime > 0 {
			anchor = max(anchor, rec.ExpiryTime-int64(*a.AppliedDurationDays)*86400000)
		}
		next := policy.NextReset(time.UnixMilli(anchor), a.AppliedResetCycle)
		if !a.OverrideQuota && !next.IsZero() && !next.After(now) {
			if !rec.Enable {
				depleted, err := depletedAndDisabled(db, []string{rec.Email})
				if err != nil {
					failures = append(failures, err)
					continue
				}
				if len(depleted) == 0 {
					continue
				}
			}
			ids, err := (&ClientService{}).GetInboundIdsForRecord(rec.ID)
			if err != nil {
				failures = append(failures, err)
				continue
			}
			if len(ids) == 0 {
				err = s.inboundService.ResetClientTrafficByEmail(rec.Email)
			} else {
				for _, id := range ids {
					nr, e := s.inboundService.ResetClientTraffic(id, rec.Email)
					out.NeedRestart = out.NeedRestart || nr
					if e != nil {
						err = e
						break
					}
				}
			}
			if err != nil {
				failures = append(failures, err)
				continue
			}
			if err := db.Model(&model.ClientPolicy{}).Where("email = ?", a.Email).Updates(map[string]any{"last_reset_at": now.UnixMilli(), "depletion_notified_at": 0}).Error; err != nil {
				failures = append(failures, err)
				continue
			}
			out.Reset++
			continue
		}
		if a.AppliedOnDepletion != "notify" || a.OverrideQuota || rec.TotalGB <= 0 {
			continue
		}
		if rec.UsedBytes < rec.TotalGB {
			if a.DepletionNotifiedAt > 0 {
				if err := db.Model(&model.ClientPolicy{}).Where("email = ?", a.Email).Update("depletion_notified_at", 0).Error; err != nil {
					failures = append(failures, err)
				}
			}
			continue
		}
		if a.DepletionNotifiedAt == 0 {
			res := db.Model(&model.ClientPolicy{}).Where("email = ? AND depletion_notified_at = 0", a.Email).Update("depletion_notified_at", now.UnixMilli())
			if res.Error != nil {
				failures = append(failures, res.Error)
				continue
			}
			if res.RowsAffected > 0 {
				out.Notices = append(out.Notices, PlanDepletionNotice{Email: a.Email, PolicyID: p.Id, PlanName: p.Name, OwnerUserID: p.OwnerUserID, UsedBytes: rec.UsedBytes, QuotaBytes: rec.TotalGB})
			}
		}
	}
	return out, errors.Join(failures...)
}
