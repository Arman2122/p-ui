package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

var planAssignmentMu sync.Mutex

type PlanApplyResult struct {
	Email       string `json:"email" example:"alice"`
	PolicyID    int    `json:"policyId" example:"1"`
	AppliedRev  int    `json:"appliedRev" example:"2"`
	NeedRestart bool   `json:"needRestart" example:"false"`
}

type PlanApplyFailure struct {
	Email  string `json:"email" example:"alice"`
	Reason string `json:"reason" example:"inbound unavailable"`
}

type PlanReapplyResult struct {
	Applied     int                `json:"applied" example:"10"`
	Failures    []PlanApplyFailure `json:"failures"`
	NeedRestart bool               `json:"needRestart" example:"false"`
}

func planClient(scope PlanScope, email string) (*model.ClientRecord, error) {
	var rec model.ClientRecord
	err := planOwner(database.GetDB(), scope, "owner_user_id").Where("email = ?", email).First(&rec).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrPlanScope
	}
	return &rec, err
}

func planExpiry(p *model.Policy, current int64, previous *model.Policy, now time.Time) int64 {
	if p.DurationDays == nil {
		return current
	}
	duration := int64(*p.DurationDays) * 86400000
	if duration == 0 {
		return 0
	}
	if previous != nil && previous.DurationDays != nil && *previous.DurationDays > 0 && current > 0 {
		return current - int64(*previous.DurationDays)*86400000 + duration
	}
	if p.StartOnFirstUse && current <= 0 {
		return -duration
	}
	return now.UnixMilli() + duration
}

// PreparePlanClient changes only the draft. Reseller callers must then debit/check their account budget.
func (s *PolicyService) PreparePlanClient(scope PlanScope, id int, client *model.Client, inboundIDs *[]int) error {
	return database.GetDB().Transaction(func(tx *gorm.DB) error { return s.PreparePlanClientTx(tx, scope, id, client, inboundIDs) })
}

// Hold the plan's read lock until its prepared client and assignment commit together.
func (s *PolicyService) PreparePlanClientTx(tx *gorm.DB, scope PlanScope, id int, client *model.Client, inboundIDs *[]int) error {
	var plan model.Policy
	if err := planOwner(tx, scope, "owner_user_id").Clauses(clause.Locking{Strength: "SHARE"}).First(&plan, id).Error; err != nil {
		return err
	}
	row := &plan
	if err := tx.Model(&model.PolicyInbound{}).Where("policy_id = ?", id).Order("inbound_id").Pluck("inbound_id", &row.InboundIDs).Error; err != nil {
		return err
	}
	if row.Archived {
		return ErrPlanArchived
	}
	if row.QuotaBytes != nil {
		client.TotalGB = *row.QuotaBytes
	}
	if row.DurationDays != nil {
		client.ExpiryTime = planExpiry(row, 0, nil, time.Now())
		client.Reset = row.AutoRenewDays
	}
	if row.LimitIP != nil {
		client.LimitIP = *row.LimitIP
	}
	if len(row.InboundIDs) > 0 {
		*inboundIDs = slices.Clone(row.InboundIDs)
	}
	return validatePlanInbounds(tx, scope, row, *inboundIDs)
}

type PlanOverrides struct {
	Quota    *bool `json:"quota" example:"true"`
	Expiry   *bool `json:"expiry" example:"true"`
	LimitIP  *bool `json:"limitIp" example:"true"`
	Inbounds *bool `json:"inbounds" example:"true"`
}

func (s *PolicyService) SetPlanOverridesTx(tx *gorm.DB, scope PlanScope, email string, overrides PlanOverrides) error {
	var count int64
	if err := planOwner(tx.Model(&model.ClientRecord{}), scope, "owner_user_id").Where("email = ?", email).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return ErrPlanScope
	}
	updates := map[string]any{}
	for column, value := range map[string]*bool{"override_quota": overrides.Quota, "override_expiry": overrides.Expiry, "override_limit_ip": overrides.LimitIP, "override_inbounds": overrides.Inbounds} {
		if value != nil {
			updates[column] = *value
			if !*value {
				updates["applied_rev"] = 0
			}
		}
	}
	if len(updates) == 0 {
		return nil
	}
	return tx.Model(&model.ClientPolicy{}).Where("email = ?", email).Updates(updates).Error
}

func (s *PolicyService) UnassignPlanTx(tx *gorm.DB, scope PlanScope, email string) error {
	var count int64
	if err := planOwner(tx.Model(&model.ClientRecord{}), scope, "owner_user_id").Where("email = ?", email).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return ErrPlanScope
	}
	return tx.Where("email = ?", email).Delete(&model.ClientPolicy{}).Error
}

// RecordPreparedPlanAssignment is for budget-checked creation; it never changes client settings.
func (s *PolicyService) RecordPreparedPlanAssignment(scope PlanScope, email string, id int) error {
	return database.GetDB().Transaction(func(tx *gorm.DB) error { return s.RecordPreparedPlanAssignmentTx(tx, scope, email, id) })
}

// Call in the same transaction that first persists a prepared client, before runtime provisioning.
func (s *PolicyService) RecordPreparedPlanAssignmentTx(tx *gorm.DB, scope PlanScope, email string, id int) error {
	var row model.Policy
	if err := planOwner(tx, scope, "owner_user_id").Clauses(clause.Locking{Strength: "SHARE"}).First(&row, id).Error; err != nil {
		return err
	}
	if row.Archived {
		return ErrPlanArchived
	}
	var count int64
	if err := planOwner(tx.Model(&model.ClientRecord{}), scope, "owner_user_id").Where("email = ?", email).Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return ErrPlanScope
	}
	a := model.ClientPolicy{Email: email, PolicyId: &id, AppliedRev: row.Rev, AppliedDurationDays: row.DurationDays, AppliedOnDepletion: row.OnDepletion, AppliedResetCycle: row.ResetCycle, LastResetAt: time.Now().UnixMilli()}
	return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "email"}}, UpdateAll: true}).Create(&a).Error
}

func (s *PolicyService) ApplyPlan(ctx context.Context, scope PlanScope, email string, id int, clearOverrides bool) (PlanApplyResult, error) {
	return s.applyPlan(ctx, scope, email, id, clearOverrides, nil)
}

// Override intent is merged under the assignment lock before provisioning or changing any limit.
func (s *PolicyService) ApplyPlanWithOverrides(ctx context.Context, scope PlanScope, email string, id int, overrides *PlanOverrides) (PlanApplyResult, error) {
	return s.applyPlan(ctx, scope, email, id, false, overrides)
}

func (s *PolicyService) applyPlan(ctx context.Context, scope PlanScope, email string, id int, clearOverrides bool, overrides *PlanOverrides) (PlanApplyResult, error) {
	result := PlanApplyResult{Email: email, PolicyID: id}
	if scope.OwnerUserID != nil {
		return result, errors.New("reseller assignment requires the budget-checked client workflow")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	planAssignmentMu.Lock()
	defer planAssignmentMu.Unlock()
	rec, err := planClient(scope, email)
	if err != nil {
		return result, err
	}
	db := database.GetDB()
	if id == 0 {
		return result, db.Where("email = ?", email).Delete(&model.ClientPolicy{}).Error
	}
	p, err := s.GetPlan(scope, id)
	if err != nil {
		return result, err
	}
	var a model.ClientPolicy
	err = db.Where("email = ?", email).First(&a).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return result, err
	}
	same := a.PolicyId != nil && *a.PolicyId == id
	if p.Archived && !same {
		return result, ErrPlanArchived
	}
	var previous *model.Policy
	if same {
		previous = &model.Policy{DurationDays: a.AppliedDurationDays}
	}
	if clearOverrides {
		a.OverrideQuota = false
		a.OverrideExpiry = false
		a.OverrideLimitIP = false
		a.OverrideInbounds = false
	}
	if overrides != nil {
		if overrides.Quota != nil {
			a.OverrideQuota = *overrides.Quota
		}
		if overrides.Expiry != nil {
			a.OverrideExpiry = *overrides.Expiry
		}
		if overrides.LimitIP != nil {
			a.OverrideLimitIP = *overrides.LimitIP
		}
		if overrides.Inbounds != nil {
			a.OverrideInbounds = *overrides.Inbounds
		}
	}
	clientSvc := &ClientService{}
	currentIDs, err := clientSvc.GetInboundIdsForRecord(rec.Id)
	if err != nil {
		return result, err
	}
	desiredIDs := currentIDs
	if !a.OverrideInbounds && len(p.InboundIDs) > 0 {
		desiredIDs = p.InboundIDs
	}
	validationPlan := *p
	if a.OverrideQuota {
		validationPlan.OnDepletion = "disable"
	}
	if err := validatePlanInbounds(db, scope, &validationPlan, desiredIDs); err != nil {
		return result, err
	}
	a.Email = email
	a.PolicyId = &id
	a.ResolvedTiers = nil
	a.AppliedRev = 0
	a.AppliedOnDepletion = "disable"
	a.AppliedResetCycle = "none"
	if !same {
		a.LastResetAt = time.Now().UnixMilli()
		a.DepletionNotifiedAt = 0
	}
	if err := db.Save(&a).Error; err != nil {
		return result, err
	}
	entry := &bulkAdjustEntry{record: rec, patch: &BulkSetPatch{}}
	if !a.OverrideQuota && p.QuotaBytes != nil {
		entry.applyTotal = true
		entry.newTotal = *p.QuotaBytes
	}
	if !a.OverrideExpiry && p.DurationDays != nil {
		entry.applyExpiry = true
		entry.newExpiry = planExpiry(p, rec.ExpiryTime, previous, time.Now())
		entry.patch.Reset = &p.AutoRenewDays
	}
	if !a.OverrideLimitIP {
		entry.patch.LimitIP = p.LimitIP
	}
	skipped := map[string]string{}
	if entry.applyExpiry || entry.applyTotal || entry.patch.Reset != nil || entry.patch.LimitIP != nil {
		exec, err := clientSvc.bulkExecutePlan(&s.inboundService, map[string]*bulkAdjustEntry{email: entry}, "", skipped)
		result.NeedRestart = exec.needRestart
		if err != nil {
			return result, err
		}
	}
	if reason, failed := skipped[email]; failed {
		return result, errors.New(reason)
	}
	updates := map[string]any{}
	if entry.applyTotal {
		updates["total_gb"] = entry.newTotal
	}
	if entry.applyExpiry {
		updates["expiry_time"] = entry.newExpiry
		updates["reset"] = p.AutoRenewDays
	}
	if entry.patch.LimitIP != nil {
		updates["limit_ip"] = *entry.patch.LimitIP
	}
	if len(updates) > 0 {
		err := runSerializedTx(func(tx *gorm.DB) error {
			if err := tx.Model(&model.ClientRecord{}).Where("id = ?", rec.Id).Updates(updates).Error; err != nil {
				return err
			}
			if entry.applyExpiry {
				if err := tx.Model(&model.ClientPolicy{}).Where("email = ?", email).Update("applied_duration_days", p.DurationDays).Error; err != nil {
					return err
				}
			}
			if entry.patch.Reset != nil {
				return tx.Table("client_traffics").Where("email = ?", email).Update("reset", *entry.patch.Reset).Error
			}
			return nil
		})
		if err != nil {
			return result, err
		}
	}
	if !a.OverrideInbounds && len(p.InboundIDs) > 0 {
		add := slices.DeleteFunc(slices.Clone(desiredIDs), func(id int) bool { return slices.Contains(currentIDs, id) })
		remove := slices.DeleteFunc(slices.Clone(currentIDs), func(id int) bool { return slices.Contains(desiredIDs, id) })
		if len(add) > 0 {
			nr, e := clientSvc.Attach(&s.inboundService, rec.Id, add)
			result.NeedRestart = result.NeedRestart || nr
			if e != nil {
				return result, e
			}
		}
		if len(remove) > 0 {
			nr, e := clientSvc.Detach(&s.inboundService, rec.Id, remove)
			result.NeedRestart = result.NeedRestart || nr
			if e != nil {
				return result, e
			}
		}
	}
	if len(updates) > 0 {
		if err := s.applyPlanRuntime(ctx, email, desiredIDs); err != nil {
			return result, err
		}
	}
	result.AppliedRev = p.Rev
	appliedDuration := a.AppliedDurationDays
	if entry.applyExpiry {
		appliedDuration = p.DurationDays
	}
	err = db.Model(&model.ClientPolicy{}).Where("email = ? AND policy_id = ?", email, id).Updates(map[string]any{"applied_rev": p.Rev, "applied_duration_days": appliedDuration, "applied_on_depletion": p.OnDepletion, "applied_reset_cycle": p.ResetCycle}).Error
	return result, err
}

// Native quotas must land before the assignment is marked applied; remote failures stay pending.
func (s *PolicyService) applyPlanRuntime(ctx context.Context, email string, inboundIDs []int) error {
	for _, id := range inboundIDs {
		ib, err := s.inboundService.GetInbound(id)
		if err != nil {
			return err
		}
		if ib.NodeID == nil {
			reg := registry()
			if reg == nil {
				continue
			}
			bound, ok := reg.For(core.Kind(ib.Protocol))
			if !ok || (bound.Quota == nil && bound.UserSet == nil) {
				continue
			}
		}
		rt, err := s.inboundService.runtimeFor(ib)
		if err != nil {
			return err
		}
		clients, err := s.inboundService.GetClients(ib)
		if err != nil {
			return err
		}
		found := false
		for _, client := range clients {
			if client.Email == email {
				found = true
				if err := rt.UpdateUser(ctx, ib, email, client); err != nil {
					return fmt.Errorf("inbound %d: %w", id, err)
				}
				break
			}
		}
		if !found {
			return fmt.Errorf("inbound %d: client %s is missing", id, email)
		}
	}
	return nil
}

func (s *PolicyService) ReapplyPlan(ctx context.Context, scope PlanScope, id int, clearOverrides bool) (PlanReapplyResult, error) {
	result := PlanReapplyResult{Failures: []PlanApplyFailure{}}
	if scope.OwnerUserID != nil {
		return result, ErrPlanScope
	}
	if _, err := s.GetPlan(scope, id); err != nil {
		return result, err
	}
	var emails []string
	err := planOwner(database.GetDB().Table("client_policies cp"), scope, "c.owner_user_id").Joins("JOIN clients c ON c.email=cp.email").Where("cp.policy_id = ?", id).Order("cp.email").Pluck("cp.email", &emails).Error
	if err != nil {
		return result, err
	}
	for _, email := range emails {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		applied, err := s.ApplyPlan(ctx, scope, email, id, clearOverrides)
		result.NeedRestart = result.NeedRestart || applied.NeedRestart
		if err != nil {
			result.Failures = append(result.Failures, PlanApplyFailure{Email: email, Reason: err.Error()})
		} else {
			result.Applied++
		}
	}
	return result, nil
}

type PlanMember struct {
	Email            string `json:"email" example:"alice"`
	Group            string `json:"group" example:"family"`
	Enable           bool   `json:"enable" example:"true"`
	UsedBytes        int64  `json:"usedBytes" example:"1073741824"`
	QuotaBytes       int64  `json:"quotaBytes" example:"53687091200"`
	ExpiryTime       int64  `json:"expiryTime" example:"1700000000000"`
	AppliedRev       int    `json:"appliedRev" example:"1"`
	Pending          bool   `json:"pending" example:"false"`
	OverrideQuota    bool   `json:"overrideQuota" example:"false"`
	OverrideExpiry   bool   `json:"overrideExpiry" example:"false"`
	OverrideLimitIP  bool   `json:"overrideLimitIp" example:"false"`
	OverrideInbounds bool   `json:"overrideInbounds" example:"false"`
}

type PlanMemberPage struct {
	Items []PlanMember `json:"items"`
	Total int64        `json:"total" example:"12"`
}

func (s *PolicyService) PlanMembers(scope PlanScope, id, page, pageSize int) (PlanMemberPage, error) {
	out := PlanMemberPage{Items: []PlanMember{}}
	if page < 1 || pageSize < 1 || pageSize > 100 {
		return out, fmt.Errorf("page must be positive and pageSize between 1 and 100")
	}
	if _, err := s.GetPlan(scope, id); err != nil {
		return out, err
	}
	db := database.GetDB()
	query := func() *gorm.DB {
		return planOwner(db.Table("client_policies cp"), scope, "c.owner_user_id").Joins("JOIN clients c ON c.email=cp.email").Joins("JOIN policies p ON p.id=cp.policy_id").Joins("LEFT JOIN client_traffics ON client_traffics.email=c.email").Where("cp.policy_id = ?", id)
	}
	if err := query().Count(&out.Total).Error; err != nil {
		return out, err
	}
	used, args := UsedBytesExpr(db)
	err := query().Select("c.email,c.group_name AS group,c.enable,c.total_gb AS quota_bytes,c.expiry_time,cp.applied_rev,(cp.applied_rev < p.rev) AS pending,cp.override_quota,cp.override_expiry,cp.override_limit_ip,cp.override_inbounds,COALESCE("+used+",0) AS used_bytes", args...).Order("c.email").Offset((page - 1) * pageSize).Limit(pageSize).Scan(&out.Items).Error
	return out, err
}
