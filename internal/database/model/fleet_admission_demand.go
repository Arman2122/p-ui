package model

type FleetAdmissionDemand struct {
	PlacementID          string `gorm:"type:uuid;primaryKey"`
	NodeID               string `gorm:"type:uuid;not null;index:idx_fleet_admission_due,priority:1,where:pending"`
	InstallationID       string `gorm:"type:uuid;not null"`
	AgentStateJournalID  string `gorm:"type:uuid;not null"`
	StateJournalID       string `gorm:"type:uuid;not null"`
	CreditJournalID      string `gorm:"type:uuid;not null"`
	ClientID             string `gorm:"type:uuid;not null;index"`
	TermID               string `gorm:"type:uuid;not null"`
	RuntimeID            string `gorm:"size:48;not null"`
	InboundInstanceID    string `gorm:"type:uuid;not null"`
	InboundTag           string `gorm:"size:128;not null"`
	RequestID            string `gorm:"type:uuid;not null"`
	RequestDigest        []byte `gorm:"not null;check:fleet_admission_request_digest,octet_length(request_digest) = 32"`
	SourcePolicyDigest   []byte `gorm:"not null;check:fleet_admission_policy_digest,octet_length(source_policy_digest) = 32"`
	DeploymentDigest     []byte `gorm:"not null;check:fleet_admission_deployment_digest,octet_length(deployment_digest) = 32"`
	DescriptorDigest     []byte `gorm:"not null;check:fleet_admission_descriptor_digest,octet_length(descriptor_digest) = 32"`
	Revision             int64  `gorm:"not null;check:fleet_admission_revision,revision > 0"`
	SessionGeneration    int64  `gorm:"not null;check:fleet_admission_session,session_generation > 0"`
	PlacementRevision    int64  `gorm:"not null;check:fleet_admission_placement,placement_revision > 0"`
	CredentialGeneration int64  `gorm:"not null;check:fleet_admission_credentials,credential_generation > 0"`
	DeploymentRevision   int64  `gorm:"not null;check:fleet_admission_deployment,deployment_revision > 0"`
	ReceivedUnixMilli    int64  `gorm:"not null"`
	DeadlineUnixMilli    int64  `gorm:"not null;check:fleet_admission_deadline,deadline_unix_milli > received_unix_milli"`
	AvailableUnixMilli   int64  `gorm:"not null;index:idx_fleet_admission_due,priority:2,where:pending"`
	Attempts             int    `gorm:"not null;check:fleet_admission_attempts,attempts >= 0 AND attempts <= 5"`
	Pending              bool   `gorm:"not null"`
	Outcome              string `gorm:"size:32;not null;default:''"`
	Diagnostic           string `gorm:"size:1024;not null;default:''"`
}

// Targets are validated at execution; cross-node foreign keys invert the node/client lock order.
type FleetRateReconciliation struct {
	GrantID            string `gorm:"type:uuid;primaryKey"`
	NodeID             string `gorm:"type:uuid;not null;index:idx_fleet_rate_reconcile_due,priority:1,where:pending"`
	PlacementID        string `gorm:"type:uuid;not null"`
	RequestID          string `gorm:"type:uuid;not null"`
	DemandRevision     int64  `gorm:"not null;check:fleet_rate_reconcile_revision,demand_revision > 0"`
	DeadlineUnixMilli  int64  `gorm:"not null;index:idx_fleet_rate_reconcile_expiry,where:pending"`
	AvailableUnixMilli int64  `gorm:"not null;index:idx_fleet_rate_reconcile_due,priority:2,where:pending"`
	Pending            bool   `gorm:"not null"`
}

type FleetAdmissionNodeQueue struct {
	NodeID              string  `gorm:"type:uuid;primaryKey"`
	Pending             bool    `gorm:"not null"`
	AvailableUnixMilli  int64   `gorm:"not null;index:idx_fleet_admission_nodes,where:pending"`
	ClaimID             *string `gorm:"type:uuid"`
	LeaseUntilUnixMilli int64   `gorm:"not null;check:fleet_admission_lease,lease_until_unix_milli >= 0"`
}
