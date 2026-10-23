package model

type FleetCreditCandidate struct {
	GrantID            string `gorm:"type:uuid;primaryKey;index:idx_fleet_credit_candidates_due,priority:3"`
	NodeID             string `gorm:"type:uuid;not null;index:idx_fleet_credit_candidates_due,priority:1"`
	AuthoritySequence  int64  `gorm:"not null;check:fleet_credit_candidate_sequence,authority_sequence > 0"`
	AvailableUnixMilli int64  `gorm:"not null;index:idx_fleet_credit_candidates_due,priority:2"`
	Attempts           int    `gorm:"not null;default:0;check:fleet_credit_candidate_attempts,attempts >= 0 AND attempts <= 5"`
}

type FleetCreditNodeQueue struct {
	NodeID              string  `gorm:"type:uuid;primaryKey;index:idx_fleet_credit_nodes_due,priority:2"`
	Pending             bool    `gorm:"not null;default:false"`
	AvailableUnixMilli  int64   `gorm:"not null;default:0;index:idx_fleet_credit_nodes_due,priority:1,where:pending"`
	ClaimID             *string `gorm:"type:uuid"`
	LeaseUntilUnixMilli int64   `gorm:"not null;default:0"`
}
