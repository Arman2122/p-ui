package service

import (
	"context"
	"errors"
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

// The scheduler publishes returned notices on its event bus; delivery and subscription policy stay there.
func (s *PolicyService) ConvergePlanCycles(ctx context.Context, now time.Time) (PlanCycleResult, error) {
	out := PlanCycleResult{Notices: []PlanDepletionNotice{}}
	planAssignmentMu.Lock()
	defer planAssignmentMu.Unlock()
	db := database.GetDB()
	var assignments []model.ClientPolicy
	if err := db.Joins("JOIN policies p ON p.id=client_policies.policy_id").Where("client_policies.applied_reset_cycle <> 'none' OR client_policies.applied_on_depletion = 'notify'").Find(&assignments).Error; err != nil {
		return out, err
	}
	plans := map[int]*model.Policy{}
	var failures []error
	for _, a := range assignments {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		p := plans[*a.PolicyId]
		if p == nil {
			p = &model.Policy{}
			if err := db.First(p, *a.PolicyId).Error; err != nil {
				failures = append(failures, err)
				continue
			}
			plans[p.Id] = p
		}
		var rec model.ClientRecord
		if err := db.Where("email = ?", a.Email).First(&rec).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				failures = append(failures, err)
			}
			continue
		}
		if rec.ExpiryTime < 0 || (rec.ExpiryTime > 0 && rec.ExpiryTime <= now.UnixMilli()) {
			continue
		}
		var ownerAllowed bool
		if err := db.Table("client_traffics").Select(ResellerClientEnabledSQL(), now.UnixMilli()).Where("email = ?", rec.Email).Scan(&ownerAllowed).Error; err != nil {
			failures = append(failures, err)
			continue
		}
		if !ownerAllowed {
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
			ids, err := (&ClientService{}).GetInboundIdsForRecord(rec.Id)
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
			a.DepletionNotifiedAt = 0
		}
		if a.AppliedOnDepletion != "notify" || a.OverrideQuota || rec.TotalGB <= 0 {
			continue
		}
		used, args := UsedBytesExpr(db)
		var usedBytes int64
		if err := db.Table("client_traffics").Select(used, args...).Where("email = ?", a.Email).Scan(&usedBytes).Error; err != nil {
			failures = append(failures, err)
			continue
		}
		if usedBytes < rec.TotalGB {
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
				out.Notices = append(out.Notices, PlanDepletionNotice{Email: a.Email, PolicyID: p.Id, PlanName: p.Name, OwnerUserID: p.OwnerUserID, UsedBytes: usedBytes, QuotaBytes: rec.TotalGB})
			}
		}
	}
	return out, errors.Join(failures...)
}
