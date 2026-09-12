package service

import "gorm.io/gorm"

type ClientPlanMetadata struct {
	PolicyID         *int   `json:"policyId" example:"1"`
	Name             string `json:"name" example:"Monthly 50 GiB"`
	Unresolved       bool   `json:"unresolved" example:"false"`
	Pending          bool   `json:"pending" example:"false"`
	Overridden       bool   `json:"overridden" example:"true"`
	OverrideQuota    bool   `json:"overrideQuota" example:"false"`
	OverrideExpiry   bool   `json:"overrideExpiry" example:"false"`
	OverrideLimitIP  bool   `json:"overrideLimitIp" example:"false"`
	OverrideInbounds bool   `json:"overrideInbounds" example:"false"`
}

func readClientPlans(db *gorm.DB, emails []string) (map[string]*ClientPlanMetadata, error) {
	out := make(map[string]*ClientPlanMetadata, len(emails))
	if len(emails) == 0 {
		return out, nil
	}
	var rows []struct {
		Email              string
		ClientPlanMetadata `gorm:"embedded"`
	}
	err := db.Table("client_policies AS cp").Joins("LEFT JOIN policies p ON p.id = cp.policy_id").
		Select(`cp.email, cp.policy_id, cp.override_quota, cp.override_expiry, cp.override_limit_ip, cp.override_inbounds, COALESCE(p.name, '') AS name,
			(p.id IS NULL AND cp.resolved_tiers IS NULL) AS unresolved,
			(COALESCE(p.rev, 0) > cp.applied_rev) AS pending,
			(cp.override_quota OR cp.override_expiry OR cp.override_limit_ip OR cp.override_inbounds) AS overridden`).
		Where("cp.email IN ?", emails).Scan(&rows).Error
	for i := range rows {
		out[rows[i].Email] = &rows[i].ClientPlanMetadata
	}
	return out, err
}
