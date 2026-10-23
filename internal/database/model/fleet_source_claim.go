package model

// Claims remain reserved until every old authorization that could use them has been retired.
type FleetSourceCredentialClaim struct {
	InboundID            int    `gorm:"primaryKey;autoIncrement:false"`
	Scheme               string `gorm:"primaryKey;size:64"`
	Digest               []byte `gorm:"primaryKey;check:fleet_source_claim_digest,octet_length(digest) = 32"`
	ClientID             string `gorm:"type:uuid;not null;index"`
	CredentialGeneration int64  `gorm:"not null;check:fleet_source_claim_generation,credential_generation > 0"`
}
