package service

import (
	"slices"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/entity"
)

func prepareFleetCreatePlan(tx *gorm.DB, request entity.ClientCreatePayload, client *model.Client) ([]int, error) {
	explicit := slices.Clone(request.InboundIds)
	if request.PolicyID == nil {
		return explicit, nil
	}
	var policy model.Policy
	if err := planOwner(tx, PlanScope{}, "owner_user_id").Clauses(clause.Locking{Strength: "SHARE"}).First(&policy, *request.PolicyID).Error; err != nil {
		return nil, err
	}
	if policy.Archived || policy.Burst {
		return nil, ErrFleetSource
	}
	if err := tx.Model(&model.PolicyInbound{}).Where("policy_id = ?", policy.Id).Order("inbound_id").Limit(4097).Pluck("inbound_id", &policy.InboundIDs).Error; err != nil {
		return nil, err
	}
	if len(policy.InboundIDs) > 4096 {
		return nil, ErrFleetSource
	}
	preparedIDs := slices.Clone(explicit)
	preparePlanClientFields(&policy, client, &preparedIDs)
	overrides := request.PlanOverrides
	if overrides != nil {
		if overrides.Quota != nil && *overrides.Quota {
			client.TotalGB = request.Client.TotalGB
		}
		if overrides.Expiry != nil && *overrides.Expiry {
			client.ExpiryTime, client.Reset = request.Client.ExpiryTime, request.Client.Reset
		}
		if overrides.LimitIP != nil && *overrides.LimitIP {
			client.LimitIP = request.Client.LimitIP
		}
	}
	return explicit, nil
}

func recordFleetCreatePlan(tx *gorm.DB, request entity.ClientCreatePayload, email string) (*model.ClientPolicy, error) {
	if request.PolicyID == nil {
		return nil, nil
	}
	if err := (&PolicyService{}).RecordPreparedPlanAssignmentTx(tx, PlanScope{}, email, *request.PolicyID); err != nil {
		return nil, err
	}
	updates := map[string]any{}
	if overrides := request.PlanOverrides; overrides != nil {
		for name, value := range map[string]*bool{"override_quota": overrides.Quota, "override_expiry": overrides.Expiry, "override_limit_ip": overrides.LimitIP, "override_inbounds": overrides.Inbounds} {
			if value != nil {
				updates[name] = *value
			}
		}
	}
	if len(updates) > 0 {
		if err := tx.Model(&model.ClientPolicy{}).Where("email = ?", email).Updates(updates).Error; err != nil {
			return nil, err
		}
	}
	var assignment model.ClientPolicy
	if err := tx.First(&assignment, "email = ?", email).Error; err != nil {
		return nil, err
	}
	return &assignment, nil
}
