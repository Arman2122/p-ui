package model

type FleetNodeJournal struct {
	NodeID           string  `gorm:"type:uuid;primaryKey"`
	InstallationID   string  `gorm:"type:uuid;not null"`
	StateJournalID   *string `gorm:"type:uuid;uniqueIndex"`
	RecoveryRequired bool    `gorm:"not null;default:false"`
	Reason           string  `gorm:"size:96;not null;default:''"`
}

type FleetRuntimeJournal struct {
	NodeID            string `gorm:"type:uuid;primaryKey"`
	RuntimeID         string `gorm:"size:48;primaryKey"`
	InstallationID    string `gorm:"type:uuid;not null"`
	CoreID            string `gorm:"size:48;not null"`
	StateJournalID    string `gorm:"type:uuid;not null;uniqueIndex"`
	CreditJournalID   string `gorm:"type:uuid;not null;uniqueIndex"`
	Sequence          int64  `gorm:"not null;default:0;check:fleet_authority_sequence,sequence >= 0"`
	Digest            []byte
	Ready             bool  `gorm:"not null;default:false"`
	ReceivedUnixMilli int64 `gorm:"not null;default:0"`
	ObservedUnixMilli int64 `gorm:"not null;default:0"`
}

type FleetGrantAuthority struct {
	GrantID             string `gorm:"type:uuid;primaryKey"`
	NodeID              string `gorm:"type:uuid;not null;index:idx_fleet_authority_runtime"`
	RuntimeID           string `gorm:"size:48;not null;index:idx_fleet_authority_runtime"`
	ReportSequence      int64  `gorm:"not null;check:fleet_authority_report_sequence,report_sequence > 0"`
	PolicyRevision      int64  `gorm:"not null;check:fleet_authority_policy_revision,policy_revision > 0"`
	AuthorityRevision   int64  `gorm:"not null;default:0;check:fleet_observed_authority_revision,authority_revision >= 0"`
	CreditCeiling       int64  `gorm:"not null;check:fleet_authority_ceiling,credit_ceiling >= 0"`
	QuotaUnlimited      bool   `gorm:"not null"`
	ReservedHighWater   int64  `gorm:"not null;check:fleet_authority_reserved,reserved_high_water >= 0"`
	BurnedHighWater     int64  `gorm:"not null;check:fleet_authority_burned,burned_high_water >= 0"`
	AdmissionHighWater  int64  `gorm:"not null;check:fleet_authority_admitted,admission_high_water >= 0"`
	AdmissionKnown      bool   `gorm:"not null"`
	SpendableBytes      int64  `gorm:"not null;check:fleet_authority_spendable,spendable_bytes >= 0"`
	SpendableKnown      bool   `gorm:"not null"`
	AuthorizationActive bool   `gorm:"not null"`
	ReceivedUnixMilli   int64  `gorm:"not null;index"`
}
