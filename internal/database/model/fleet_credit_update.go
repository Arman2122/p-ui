package model

type FleetCreditUpdate struct {
	GrantID                   string `gorm:"type:uuid;primaryKey;index:idx_fleet_credit_delivery,priority:4"`
	NodeID                    string `gorm:"type:uuid;not null;index:idx_fleet_credit_delivery,priority:1,where:revision > acknowledged_revision"`
	InstallationID            string `gorm:"type:uuid;not null"`
	RuntimeID                 string `gorm:"size:48;not null;index:idx_fleet_credit_delivery,priority:2"`
	StateJournalID            string `gorm:"type:uuid;not null"`
	CreditJournalID           string `gorm:"type:uuid;not null"`
	ClientID                  string `gorm:"type:uuid;not null"`
	TermID                    string `gorm:"type:uuid;not null"`
	BaseConfigurationRevision int64  `gorm:"not null;index:idx_fleet_credit_delivery,priority:3;check:fleet_credit_base,base_configuration_revision > 0"`
	Revision                  int64  `gorm:"not null;check:fleet_credit_revision,revision > 0"`
	CreditCeiling             int64  `gorm:"not null;check:fleet_credit_ceiling,credit_ceiling >= 0"`
	AcknowledgedRevision      int64  `gorm:"not null;default:0;check:fleet_credit_ack,acknowledged_revision >= 0 AND acknowledged_revision <= revision"`
	AcknowledgedCeiling       int64  `gorm:"not null;default:0;check:fleet_credit_ack_ceiling,acknowledged_ceiling >= 0 AND acknowledged_ceiling <= credit_ceiling"`
}
