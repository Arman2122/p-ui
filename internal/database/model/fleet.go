package model

type FleetAuthority struct {
	ID             int    `gorm:"primaryKey;check:fleet_authority_singleton,id = 1"`
	ControlPlaneID string `gorm:"type:uuid;not null;uniqueIndex"`
	KeyDigest      []byte `gorm:"not null"`
}

type FleetRecoveryChallenge struct {
	NodeID           string  `gorm:"type:uuid;primaryKey"`
	InstallationID   string  `gorm:"type:uuid;not null"`
	ChallengeID      string  `gorm:"type:uuid;not null;uniqueIndex"`
	Nonce            []byte  `gorm:"not null"`
	ExpiresUnixMilli int64   `gorm:"not null"`
	RequestID        *string `gorm:"type:uuid"`
	RequestDigest    []byte
	ProofDigest      []byte
	Certificate      []byte
}

type FleetFence struct {
	ID               int     `gorm:"primaryKey;check:fleet_fence_singleton,id = 1"`
	RecoveryRequired bool    `gorm:"not null;default:false"`
	Reason           string  `gorm:"not null;default:''"`
	WitnessNodeID    *string `gorm:"type:uuid"`
}

type FleetRateBudget struct {
	ID       string `gorm:"type:uuid;primaryKey"`
	ClientID string `gorm:"type:uuid;not null;uniqueIndex:idx_fleet_rate_client"`
	TermID   string `gorm:"type:uuid;not null"`
	Revision int64  `gorm:"not null;check:fleet_rate_budget_revision,revision > 0"`
	Desired  string `gorm:"type:jsonb;not null"`
}

type FleetRateShare struct {
	ID              string `gorm:"type:uuid;primaryKey"`
	BudgetID        string `gorm:"type:uuid;not null;index"`
	NodeID          string `gorm:"type:uuid;not null;index"`
	InstallationID  string `gorm:"type:uuid;not null"`
	RuntimeID       string `gorm:"size:48;not null"`
	Revision        int64  `gorm:"not null;check:fleet_rate_share_revision,revision > 0"`
	AppliedRevision int64  `gorm:"not null;default:0;check:fleet_rate_applied,applied_revision >= 0 AND applied_revision <= revision"`
	Desired         string `gorm:"type:jsonb;not null"`
	Outstanding     string `gorm:"type:jsonb;not null"`
}

type FleetNode struct {
	ID                  string `gorm:"type:uuid;primaryKey"`
	InstallationID      string `gorm:"type:uuid;not null;uniqueIndex"`
	Name                string `gorm:"not null"`
	Enabled             bool   `gorm:"not null;default:false"`
	AuthorityGeneration int64  `gorm:"not null;default:0;check:fleet_node_authority,authority_generation >= 0"`
	DesiredRevision     int64  `gorm:"not null;default:0;check:fleet_node_desired,desired_revision >= 0"`
	AppliedRevision     int64  `gorm:"not null;default:0;check:fleet_node_applied,applied_revision >= 0 AND applied_revision <= desired_revision"`
	SentRevision        int64  `gorm:"not null;default:0"`
	LastSeenUnixMilli   int64  `gorm:"not null;default:0"`
}

type FleetDeployment struct {
	NodeID   string `gorm:"type:uuid;primaryKey"`
	Revision int64  `gorm:"primaryKey;not null;check:fleet_deployment_revision,revision > 0"`
	Digest   []byte `gorm:"not null"`
	Payload  []byte `gorm:"not null"`
}

type FleetBudget struct {
	ID            string `gorm:"type:uuid;primaryKey"`
	LimitBytes    int64  `gorm:"not null;check:fleet_budget_limit,limit_bytes >= 0"`
	Unlimited     bool   `gorm:"not null;default:false"`
	UsedBytes     int64  `gorm:"not null;default:0;check:fleet_budget_used,used_bytes >= 0"`
	ReservedBytes int64  `gorm:"not null;default:0;check:fleet_budget_balance,reserved_bytes >= 0 AND (unlimited OR (used_bytes <= limit_bytes AND reserved_bytes <= limit_bytes - used_bytes))"`
}

type FleetGrant struct {
	ID             string  `gorm:"type:uuid;primaryKey"`
	PlacementID    *string `gorm:"type:uuid;index"`
	NodeID         string  `gorm:"type:uuid;not null;index"`
	InstallationID string  `gorm:"type:uuid;not null"`
	RuntimeID      string  `gorm:"size:48;not null"`
	ClientID       string  `gorm:"type:uuid;not null;index"`
	TermID         string  `gorm:"type:uuid;not null;index"`
	Unlimited      bool    `gorm:"not null;default:false"`
	IssuedBytes    int64   `gorm:"not null;check:fleet_grant_issued,issued_bytes >= 0"`
	ConsumedBytes  int64   `gorm:"not null;default:0;check:fleet_grant_consumed,consumed_bytes >= 0 AND (unlimited OR consumed_bytes <= issued_bytes)"`
	Revision       int64   `gorm:"not null;check:fleet_grant_revision,revision > 0"`
}

type FleetGrantBudget struct {
	GrantID  string `gorm:"type:uuid;primaryKey"`
	BudgetID string `gorm:"type:uuid;primaryKey;index"`
}

type FleetUsageCursor struct {
	NodeID         string `gorm:"type:uuid;primaryKey"`
	InstallationID string `gorm:"type:uuid;primaryKey"`
	Sequence       int64  `gorm:"not null;default:0;check:fleet_cursor_sequence,sequence >= 0"`
	Digest         []byte
}

type FleetMeter struct {
	GrantID     string `gorm:"type:uuid;primaryKey"`
	MeterID     string `gorm:"size:128;primaryKey"`
	Incarnation string `gorm:"type:uuid;primaryKey"`
	Uploaded    int64  `gorm:"not null;check:fleet_meter_uploaded,uploaded >= 0"`
	Downloaded  int64  `gorm:"not null;check:fleet_meter_downloaded,downloaded >= 0"`
}

type FleetOperation struct {
	ID     string `gorm:"type:uuid;primaryKey"`
	Digest []byte `gorm:"not null"`
	Result []byte `gorm:"not null"`
}

type FleetEnrollment struct {
	ID               string  `gorm:"type:uuid;primaryKey"`
	NodeID           string  `gorm:"type:uuid;not null;index"`
	InstallationID   string  `gorm:"type:uuid;not null"`
	Digest           []byte  `gorm:"not null;uniqueIndex"`
	ExpiresUnixMilli int64   `gorm:"not null"`
	RequestID        *string `gorm:"type:uuid"`
	KeyDigest        []byte
	Certificate      []byte
}

type FleetCredential struct {
	NodeID          string `gorm:"type:uuid;primaryKey"`
	InstallationID  string `gorm:"type:uuid;not null"`
	RequestID       string `gorm:"type:uuid;not null"`
	KeyDigest       []byte `gorm:"not null"`
	Certificate     []byte `gorm:"not null"`
	IssuedUnixMilli int64  `gorm:"not null"`
	PreviousSerial  string `gorm:"size:64;not null;default:''"`
}
