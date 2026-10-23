package service

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

type ClientUpdateOptions struct {
	PolicyID      *int           `json:"policyId,omitempty" example:"1"`
	PlanOverrides *PlanOverrides `json:"planOverrides,omitempty"`
	InboundIDs    *[]int         `json:"inboundIds,omitempty" example:"[1,2]"`
}

func (s *ClientService) UpdateWithPlan(ctx context.Context, inboundSvc *InboundService, email string, updated model.Client, options ClientUpdateOptions, inboundFilter ...int) (bool, error) {
	if options.PolicyID == nil && options.PlanOverrides == nil && options.InboundIDs == nil {
		return s.UpdateByEmail(inboundSvc, email, updated, inboundFilter...)
	}
	rec, err := s.GetRecordByEmail(nil, email)
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(updated.Email) == "" {
		return false, errors.New("client email is required")
	}
	if err := validateClientEmail(updated.Email); err != nil {
		return false, err
	}
	if err := ValidateClientOwnerGroup(email, updated.Group); err != nil {
		return false, err
	}
	if updated.Enable {
		if err := ValidateClientOwnerEnable(email); err != nil {
			return false, err
		}
	}
	if options.PolicyID != nil && *options.PolicyID < 0 {
		return false, errors.New("policyId must not be negative")
	}
	scope := PlanScope{}
	policySvc := &PolicyService{}
	if options.PolicyID != nil || options.PlanOverrides != nil {
		if _, err := planClient(scope, email); err != nil {
			return false, err
		}
	}
	restart := false
	if options.PolicyID != nil {
		result, err := policySvc.ApplyPlanWithOverrides(ctx, scope, email, *options.PolicyID, options.PlanOverrides)
		restart = result.NeedRestart
		if err != nil {
			return restart, err
		}
	}
	db := database.GetDB()
	if options.PlanOverrides != nil && options.PolicyID == nil {
		var assignment model.ClientPolicy
		if err := db.Where("email = ?", email).First(&assignment).Error; err != nil {
			return restart, errors.New("plan overrides require an assigned plan")
		}
		clear := options.PlanOverrides
		if assignment.PolicyId != nil && ((clear.Quota != nil && !*clear.Quota) || (clear.Expiry != nil && !*clear.Expiry) ||
			(clear.LimitIP != nil && !*clear.LimitIP) || (clear.Inbounds != nil && !*clear.Inbounds)) {
			result, err := policySvc.ApplyPlanWithOverrides(ctx, scope, email, *assignment.PolicyId, options.PlanOverrides)
			restart = restart || result.NeedRestart
			if err != nil {
				return restart, err
			}
		} else if err := db.Transaction(func(tx *gorm.DB) error {
			return policySvc.SetPlanOverridesTx(tx, scope, email, *options.PlanOverrides)
		}); err != nil {
			return restart, err
		}
	}
	current, err := s.GetRecordByEmail(nil, email)
	if err != nil {
		return restart, err
	}
	currentIDs, err := s.GetInboundIdsForRecord(rec.Id)
	if err != nil {
		return restart, err
	}
	desiredIDs := slices.Clone(currentIDs)
	if options.InboundIDs != nil {
		desiredIDs = slices.Clone(*options.InboundIDs)
	}
	var assignment model.ClientPolicy
	err = db.Where("email = ?", email).First(&assignment).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return restart, err
	}
	if err == nil && assignment.PolicyId != nil {
		plan, err := policySvc.GetPlan(PlanScope{OwnerUserID: current.OwnerUserID}, *assignment.PolicyId)
		if err != nil {
			return restart, err
		}
		if !assignment.OverrideQuota && plan.QuotaBytes != nil {
			updated.TotalGB = current.TotalGB
		}
		if !assignment.OverrideExpiry && plan.DurationDays != nil {
			updated.ExpiryTime, updated.Reset = current.ExpiryTime, current.Reset
		}
		if !assignment.OverrideLimitIP && plan.LimitIP != nil {
			updated.LimitIP = current.LimitIP
		}
		if !assignment.OverrideInbounds && len(plan.InboundIDs) > 0 {
			desiredIDs = currentIDs
		}
	}
	if err := policySvc.ValidatePlanClientInbounds(email, desiredIDs); err != nil {
		return restart, err
	}
	if err := ValidateClientOwnerInbounds(email, desiredIDs); err != nil {
		return restart, err
	}
	remove := []int{}
	for _, id := range currentIDs {
		if !slices.Contains(desiredIDs, id) {
			remove = append(remove, id)
		}
	}
	if len(remove) > 0 {
		nr, err := s.DetachByEmailMany(inboundSvc, email, remove)
		restart = restart || nr
		if err != nil {
			return restart, err
		}
	}
	nr, err := s.AttachByEmail(inboundSvc, email, desiredIDs)
	restart = restart || nr
	if err != nil {
		return restart, err
	}
	nr, err = s.UpdateByEmail(inboundSvc, email, updated, inboundFilter...)
	restart = restart || nr
	if err != nil {
		return restart, err
	}
	if err := s.ApplyGroupInbounds(updated.Group); err != nil {
		return restart, err
	}
	return restart, s.EnforceGroupQuotas()
}

func (s *ClientService) includeGroupInbounds(payload *ClientCreatePayload) error {
	if strings.TrimSpace(payload.Client.Group) == "" {
		return nil
	}
	var ids []int
	if err := database.GetDB().Model(&model.GroupInbound{}).Where("name = ?", payload.Client.Group).Pluck("inbound_id", &ids).Error; err != nil {
		return err
	}
	payload.InboundIds = append(slices.Clone(payload.InboundIds), ids...)
	slices.Sort(payload.InboundIds)
	payload.InboundIds = slices.Compact(payload.InboundIds)
	return nil
}

func (s *ClientService) BulkCreateWithPlans(inboundSvc *InboundService, payloads []ClientCreatePayload) (BulkCreateResult, bool, error) {
	result := BulkCreateResult{}
	restart := false
	custom := []ClientCreatePayload{}
	flush := func() error {
		if len(custom) == 0 {
			return nil
		}
		part, nr, err := s.BulkCreate(inboundSvc, custom)
		restart = restart || nr
		result.Created += part.Created
		result.Skipped = append(result.Skipped, part.Skipped...)
		result.Outcome.OK = append(result.Outcome.OK, part.Outcome.OK...)
		result.Outcome.Skipped = append(result.Outcome.Skipped, part.Outcome.Skipped...)
		result.Outcome.Failed = append(result.Outcome.Failed, part.Outcome.Failed...)
		custom = custom[:0]
		return err
	}
	for i := range payloads {
		payload := &payloads[i]
		if payload.PolicyID == nil || *payload.PolicyID == 0 {
			if err := s.includeGroupInbounds(payload); err != nil {
				return result, restart, err
			}
			custom = append(custom, *payload)
			continue
		}
		if err := flush(); err != nil {
			return result, restart, err
		}
		nr, err := s.CreateWithPlan(inboundSvc, PlanScope{}, payload)
		restart = restart || nr
		if err != nil {
			result.Skipped = append(result.Skipped, BulkCreateReport{Email: payload.Client.Email, Reason: err.Error()})
			result.Outcome.fail(payload.Client.Email, err.Error())
		} else {
			result.Created++
			result.Outcome.ok(payload.Client.Email)
		}
	}
	if err := flush(); err != nil {
		return result, restart, err
	}
	result.Outcome.finalize()
	return result, restart, nil
}

// CreateWithPlan stores the prepared client and assignment together before any
// runtime call; a failed provisioning removes only the identity this call created.
func (s *ClientService) CreateWithPlan(inboundSvc *InboundService, scope PlanScope, payload *ClientCreatePayload) (bool, error) {
	if err := requireLegacyClientCreation(payload); err != nil {
		return false, err
	}
	if payload == nil || payload.PolicyID == nil || *payload.PolicyID <= 0 {
		return false, errors.New("a positive policyId is required")
	}
	if strings.TrimSpace(payload.Client.Email) == "" {
		return false, errors.New("client email is required")
	}
	if err := validateClientEmail(payload.Client.Email); err != nil {
		return false, err
	}
	if err := validateNewClientSubID(payload.Client.SubID); err != nil {
		return false, err
	}
	if payload.Client.SubID == "" {
		payload.Client.SubID = uuid.NewString()
	}
	policySvc := &PolicyService{}
	original, originalIDs := payload.Client, slices.Clone(payload.InboundIds)
	err := database.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := policySvc.PreparePlanClientTx(tx, scope, *payload.PolicyID, &payload.Client, &payload.InboundIds); err != nil {
			return err
		}
		if overrides := payload.PlanOverrides; overrides != nil {
			if overrides.Quota != nil && *overrides.Quota {
				payload.Client.TotalGB = original.TotalGB
			}
			if overrides.Expiry != nil && *overrides.Expiry {
				payload.Client.ExpiryTime, payload.Client.Reset = original.ExpiryTime, original.Reset
			}
			if overrides.LimitIP != nil && *overrides.LimitIP {
				payload.Client.LimitIP = original.LimitIP
			}
			if overrides.Inbounds != nil && *overrides.Inbounds {
				payload.InboundIds = originalIDs
			}
		}
		if len(payload.InboundIds) == 0 {
			return errors.New("at least one inbound is required")
		}
		record := payload.Client.ToRecord()
		record.OwnerUserID = scope.OwnerUserID
		if err := tx.Create(record).Error; err != nil {
			return err
		}
		if err := policySvc.RecordPreparedPlanAssignmentTx(tx, scope, payload.Client.Email, *payload.PolicyID); err != nil {
			return err
		}
		if payload.PlanOverrides != nil {
			return policySvc.SetPlanOverridesTx(tx, scope, payload.Client.Email, *payload.PlanOverrides)
		}
		return nil
	})
	if err != nil {
		return false, err
	}
	restart, err := s.create(inboundSvc, payload)
	if err == nil && len(payload.ExternalLinks) > 0 {
		err = s.SetExternalLinksByEmail(payload.Client.Email, payload.ExternalLinks)
	}
	if err == nil && payload.Client.Group != "" {
		err = s.EnforceGroupQuotas()
	}
	if err != nil {
		cleanupRestart, cleanupErr := s.DeleteByEmail(inboundSvc, payload.Client.Email, false)
		if cleanupErr != nil {
			return restart || cleanupRestart, fmt.Errorf("client provisioning failed; cleanup incomplete: %w", errors.Join(err, cleanupErr))
		}
		return restart || cleanupRestart, fmt.Errorf("client provisioning rolled back: %w", err)
	}
	return restart, nil
}
