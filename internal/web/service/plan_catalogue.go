package service

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

var (
	ErrPlanScope       = errors.New("plan or client is outside the owner scope")
	ErrPlanArchived    = errors.New("archived plans cannot receive new assignments")
	ErrPlanUnsupported = errors.New("plan behavior is unsupported by the selected core")
)

// A nil owner selects legacy administrator-owned plans; callers derive scope from authentication.
type PlanScope struct{ OwnerUserID *int }

type PlanSummary struct {
	WithoutPlan int64 `json:"withoutPlan" example:"12"`
}

func (s *PolicyService) PlanSummary(scope PlanScope) (PlanSummary, error) {
	var out PlanSummary
	err := planOwner(database.GetDB().Model(&model.ClientRecord{}), scope, "clients.owner_user_id").Where("NOT EXISTS (SELECT 1 FROM client_policies cp JOIN policies p ON p.id=cp.policy_id WHERE cp.email=clients.email)").Count(&out.WithoutPlan).Error
	return out, err
}

func planOwner(db *gorm.DB, scope PlanScope, column string) *gorm.DB {
	if scope.OwnerUserID == nil {
		return db.Where(column + " IS NULL")
	}
	return db.Where(column+" = ?", *scope.OwnerUserID)
}

func (s *PolicyService) ListPlans(scope PlanScope) ([]*model.Policy, error) {
	rows := []*model.Policy{}
	db := database.GetDB()
	if err := planOwner(db, scope, "owner_user_id").Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return rows, nil
	}
	ids := make([]int, 0, len(rows))
	byID := map[int]*model.Policy{}
	for _, row := range rows {
		ids = append(ids, row.Id)
		byID[row.Id] = row
		row.InboundIDs = []int{}
	}
	var links []model.PolicyInbound
	if err := db.Where("policy_id IN ?", ids).Order("inbound_id").Find(&links).Error; err != nil {
		return nil, err
	}
	for _, link := range links {
		byID[link.PolicyId].InboundIDs = append(byID[link.PolicyId].InboundIDs, link.InboundId)
	}
	used, args := UsedBytesExpr(db)
	var stats []struct {
		PolicyID                           int
		Assigned, Pending, Used, Committed int64
	}
	err := planOwner(db.Table("client_policies cp"), scope, "c.owner_user_id").
		Select("cp.policy_id, COUNT(*) AS assigned, COUNT(*) FILTER (WHERE cp.applied_rev < p.rev) AS pending, COALESCE(SUM("+used+"),0) AS used, COALESCE(SUM(c.total_gb),0) AS committed", args...).
		Joins("JOIN clients c ON c.email = cp.email").Joins("JOIN policies p ON p.id = cp.policy_id").
		Joins("LEFT JOIN client_traffics ON client_traffics.email = c.email").Where("cp.policy_id IN ?", ids).Group("cp.policy_id").Scan(&stats).Error
	if err != nil {
		return nil, err
	}
	for _, stat := range stats {
		p := byID[stat.PolicyID]
		p.AssignedClients = stat.Assigned
		p.PendingClients = stat.Pending
		p.UsedBytes = stat.Used
		p.CommittedBytes = stat.Committed
	}
	return rows, nil
}

func (s *PolicyService) GetPlan(scope PlanScope, id int) (*model.Policy, error) {
	var row model.Policy
	db := database.GetDB()
	err := planOwner(db, scope, "owner_user_id").First(&row, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, fmt.Errorf("%w: %d", ErrPolicyUnknown, id)
	}
	if err != nil {
		return nil, err
	}
	row.InboundIDs = []int{}
	err = db.Model(&model.PolicyInbound{}).Where("policy_id = ?", id).Order("inbound_id").Pluck("inbound_id", &row.InboundIDs).Error
	return &row, err
}

func validatePlan(row *model.Policy) error {
	row.Name = strings.TrimSpace(row.Name)
	row.Description = strings.TrimSpace(row.Description)
	if row.Name == "" || utf8.RuneCountInString(row.Name) > 128 || utf8.RuneCountInString(row.Description) > 2000 {
		return errors.New("plan name must contain 1–128 characters and description at most 2000 characters")
	}
	if row.QuotaBytes != nil && (*row.QuotaBytes < 0 || *row.QuotaBytes > 9007199254740991) {
		return errors.New("quotaBytes is outside the supported range")
	}
	if row.DurationDays != nil && (*row.DurationDays < 0 || *row.DurationDays > 36500) {
		return errors.New("durationDays must be between 0 and 36500")
	}
	if row.LimitIP != nil && (*row.LimitIP < 0 || *row.LimitIP > 1000000) {
		return errors.New("limitIp must be between 0 and 1000000")
	}
	if row.AutoRenewDays < 0 || row.AutoRenewDays > 36500 {
		return errors.New("autoRenewDays must be between 0 and 36500")
	}
	if row.AutoRenewDays > 0 && (row.DurationDays == nil || *row.DurationDays == 0) {
		return errors.New("auto renewal requires a finite managed duration")
	}
	if row.StartOnFirstUse && (row.DurationDays == nil || *row.DurationDays == 0) {
		return errors.New("first use requires a finite managed duration")
	}
	if row.ResetCycle == "" {
		row.ResetCycle = "none"
	}
	if row.OnDepletion == "" {
		row.OnDepletion = "disable"
	}
	if !slices.Contains([]string{"none", "weekly", "monthly"}, row.ResetCycle) {
		return errors.New("unknown traffic reset cycle")
	}
	if !slices.Contains([]string{"disable", "throttle", "notify"}, row.OnDepletion) {
		return errors.New("unknown depletion action")
	}
	if row.OnDepletion != "disable" && (row.QuotaBytes == nil || *row.QuotaBytes == 0) {
		return errors.New("quota continuation requires a finite managed quota")
	}
	if row.Burst {
		return fmt.Errorf("%w: no core exposes configurable connection bursts", ErrPlanUnsupported)
	}
	if err := canonicalTiers(row); err != nil {
		return err
	}
	ladder, _ := ParsePlan(row.Tiers)
	for _, tier := range ladder.Tiers {
		if tier.FromBytes < 0 || tier.UpBps < 0 || tier.DownBps < 0 {
			return ErrPolicyBadLadder
		}
	}
	slices.Sort(row.InboundIDs)
	row.InboundIDs = slices.Compact(row.InboundIDs)
	if len(row.InboundIDs) > 500 {
		return errors.New("at most 500 inbounds per plan")
	}
	return nil
}

func validatePlanInbounds(db *gorm.DB, scope PlanScope, row *model.Policy, ids []int) error {
	if len(ids) == 0 {
		if row.OnDepletion != "disable" {
			return fmt.Errorf("%w: quota continuation requires an explicit inbound set", ErrPlanUnsupported)
		}
		return nil
	}
	var inbounds []model.Inbound
	if err := db.Where("id IN ?", ids).Find(&inbounds).Error; err != nil {
		return err
	}
	if len(inbounds) != len(ids) {
		return errors.New("plan contains an unknown inbound")
	}
	if scope.OwnerUserID != nil {
		var granted int64
		if err := db.Table("reseller_inbound_grants").Where("user_id = ? AND inbound_id IN ?", *scope.OwnerUserID, ids).Count(&granted).Error; err != nil {
			return err
		}
		if granted != int64(len(ids)) {
			return ErrPlanScope
		}
	}
	for _, inbound := range inbounds {
		if row.OnDepletion == "disable" {
			continue
		}
		reg := registry()
		if reg == nil {
			return fmt.Errorf("%w: core registry is unavailable", ErrPlanUnsupported)
		}
		bound, ok := reg.For(core.Kind(inbound.Protocol))
		if !ok || bound.Quota != nil {
			return fmt.Errorf("%w: inbound %d enforces its own hard quota or its core is unavailable", ErrPlanUnsupported, inbound.Id)
		}
		if inbound.NodeID != nil {
			return fmt.Errorf("%w: quota continuation currently requires locally managed inbounds", ErrPlanUnsupported)
		}
		canRate := bound.Rate != nil && bound.Rate.EnforcesRates(core.Kind(inbound.Protocol))
		canShape := bound.Shape != nil && bound.Shape.ShapingSelector(core.Kind(inbound.Protocol)) != core.SelectorNone
		if row.OnDepletion == "throttle" && !canRate && !canShape {
			return fmt.Errorf("%w: inbound %d cannot enforce speed limits", ErrPlanUnsupported, inbound.Id)
		}
	}
	return nil
}

// Call before changing a client's full attachment set outside plan assignment.
func (s *PolicyService) ValidatePlanClientInbounds(email string, inboundIDs []int) error {
	var row model.Policy
	db := database.GetDB()
	err := db.Select("policies.id,policies.owner_user_id,cp.applied_on_depletion AS on_depletion").Joins("JOIN client_policies cp ON cp.policy_id = policies.id").Where("cp.email = ? AND NOT cp.override_quota", email).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if row.OnDepletion == "disable" {
		return nil
	}
	ids := slices.Clone(inboundIDs)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	return validatePlanInbounds(db, PlanScope{OwnerUserID: row.OwnerUserID}, &row, ids)
}

// Names are the only handle a picker shows, so two of them in one scope is a
// refusal; the check is case-insensitive because the pickers sort that way.
func refusePlanNameClash(tx *gorm.DB, scope PlanScope, row *model.Policy) error {
	var clashes int64
	err := planOwner(tx.Model(&model.Policy{}), scope, "owner_user_id").
		Where("lower(name) = lower(?) AND id <> ?", row.Name, row.Id).Count(&clashes).Error
	if err != nil {
		return err
	}
	if clashes > 0 {
		return fmt.Errorf("%w: %q", ErrPolicyNameTaken, row.Name)
	}
	return nil
}

func managedPlanEqual(a, b *model.Policy) bool {
	return reflect.DeepEqual(a.QuotaBytes, b.QuotaBytes) && reflect.DeepEqual(a.DurationDays, b.DurationDays) && reflect.DeepEqual(a.LimitIP, b.LimitIP) && slices.Equal(a.InboundIDs, b.InboundIDs) && a.StartOnFirstUse == b.StartOnFirstUse && a.AutoRenewDays == b.AutoRenewDays && a.ResetCycle == b.ResetCycle && a.OnDepletion == b.OnDepletion
}

func (s *PolicyService) SavePlan(scope PlanScope, row *model.Policy) (*model.Policy, error) {
	if err := validatePlan(row); err != nil {
		return nil, err
	}
	if scope.OwnerUserID != nil && (row.StartOnFirstUse || row.AutoRenewDays > 0) {
		return nil, fmt.Errorf("%w: reseller plans require absolute expiry and cannot renew automatically", ErrPlanUnsupported)
	}
	row.OwnerUserID = scope.OwnerUserID
	db := database.GetDB()
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := validatePlanInbounds(tx, scope, row, row.InboundIDs); err != nil {
			return err
		}
		if row.Id > 0 {
			var old model.Policy
			if err := planOwner(tx, scope, "owner_user_id").Clauses(clause.Locking{Strength: "UPDATE"}).First(&old, row.Id).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.PolicyInbound{}).Where("policy_id = ?", row.Id).Order("inbound_id").Pluck("inbound_id", &old.InboundIDs).Error; err != nil {
				return err
			}
			row.Rev = old.Rev
			row.CreatedAt = old.CreatedAt
			if !managedPlanEqual(&old, row) {
				row.Rev++
			}
		} else {
			row.Rev = 1
			row.CreatedAt = 0
			row.UpdatedAt = 0
		}
		if err := refusePlanNameClash(tx, scope, row); err != nil {
			return err
		}
		if err := tx.Save(row).Error; err != nil {
			return err
		}
		if err := tx.Where("policy_id = ?", row.Id).Delete(&model.PolicyInbound{}).Error; err != nil {
			return err
		}
		for _, id := range row.InboundIDs {
			if err := tx.Create(&model.PolicyInbound{PolicyId: row.Id, InboundId: id}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.GetPlan(scope, row.Id)
}

func (s *PolicyService) ArchivePlan(scope PlanScope, id int, archived bool) (*model.Policy, error) {
	if _, err := s.GetPlan(scope, id); err != nil {
		return nil, err
	}
	if err := planOwner(database.GetDB().Model(&model.Policy{}), scope, "owner_user_id").Where("id = ?", id).Update("archived", archived).Error; err != nil {
		return nil, err
	}
	return s.GetPlan(scope, id)
}

func (s *PolicyService) DuplicatePlan(scope PlanScope, id int, name string) (*model.Policy, error) {
	row, err := s.GetPlan(scope, id)
	if err != nil {
		return nil, err
	}
	row.Id = 0
	row.Name = name
	row.Archived = false
	return s.SavePlan(scope, row)
}

func (s *PolicyService) DeletePlan(scope PlanScope, id int) error {
	if _, err := s.GetPlan(scope, id); err != nil {
		return err
	}
	return planOwner(database.GetDB(), scope, "owner_user_id").Delete(&model.Policy{}, id).Error
}
