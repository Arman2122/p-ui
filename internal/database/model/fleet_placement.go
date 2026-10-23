package model

type FleetPlacement struct {
	ID                   string `gorm:"type:uuid;primaryKey"`
	ClientID             string `gorm:"type:uuid;not null;uniqueIndex:idx_fleet_placement_owner"`
	NodeID               string `gorm:"type:uuid;not null;uniqueIndex:idx_fleet_placement_owner;index"`
	InstallationID       string `gorm:"type:uuid;not null;uniqueIndex:idx_fleet_placement_owner"`
	RuntimeID            string `gorm:"size:48;not null;uniqueIndex:idx_fleet_placement_owner"`
	CoreID               string `gorm:"size:48;not null"`
	Revision             int64  `gorm:"not null;check:fleet_placement_revision,revision > 0"`
	Enabled              bool   `gorm:"not null;default:false"`
	CredentialGeneration int64  `gorm:"not null;check:fleet_placement_credentials,credential_generation > 0"`
	SourcePolicyDigest   []byte `gorm:"not null;check:fleet_placement_policy,octet_length(source_policy_digest) = 32"`
	DefinitionDigest     []byte `gorm:"not null;check:fleet_placement_definition,octet_length(definition_digest) = 32"`
	GraphDigest          []byte
	CurrentGrantID       *string `gorm:"type:uuid"`
	CurrentGrantTermID   *string `gorm:"type:uuid"`
}

type FleetPlacementInbound struct {
	PlacementID  string `gorm:"type:uuid;primaryKey;uniqueIndex:idx_fleet_placement_inbound"`
	InstanceID   string `gorm:"type:uuid;primaryKey"`
	PanelInbound *int   `gorm:"uniqueIndex:idx_fleet_placement_inbound;index"`
}

type FleetBudgetScope struct {
	BudgetID    string `gorm:"type:uuid;primaryKey"`
	Kind        string `gorm:"not null;uniqueIndex:idx_fleet_budget_source;check:fleet_budget_kind,kind IN ('group','reseller')"`
	SourceID    int    `gorm:"not null;uniqueIndex:idx_fleet_budget_source;check:fleet_budget_source,source_id > 0"`
	SourceEpoch int64  `gorm:"not null;default:0"`
}
