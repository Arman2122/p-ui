package model

type FleetAuthorityUpdate struct {
	GrantID                   string `gorm:"type:uuid;primaryKey;index:idx_fleet_authority_delivery,priority:4"`
	NodeID                    string `gorm:"type:uuid;not null;uniqueIndex:idx_fleet_authority_client_base;index:idx_fleet_authority_delivery,priority:1,where:revision > acknowledged_revision AND NOT delivery_blocked"`
	InstallationID            string `gorm:"type:uuid;not null"`
	RuntimeID                 string `gorm:"size:48;not null;uniqueIndex:idx_fleet_authority_client_base;index:idx_fleet_authority_delivery,priority:2"`
	AgentStateJournalID       string `gorm:"type:uuid;not null"`
	StateJournalID            string `gorm:"type:uuid;not null"`
	CreditJournalID           string `gorm:"type:uuid;not null"`
	ClientID                  string `gorm:"type:uuid;not null;uniqueIndex:idx_fleet_authority_client_base"`
	TermID                    string `gorm:"type:uuid;not null"`
	PlacementID               string `gorm:"type:uuid;not null"`
	BaseConfigurationRevision int64  `gorm:"not null;uniqueIndex:idx_fleet_authority_client_base;index:idx_fleet_authority_delivery,priority:3;check:fleet_authority_base,base_configuration_revision > 0"`
	BasePayloadSHA256         []byte `gorm:"not null;check:fleet_authority_base_digest,octet_length(base_payload_sha256) = 32"`
	DescriptorSHA256          []byte `gorm:"not null;check:fleet_authority_descriptor_digest,octet_length(descriptor_sha256) = 32"`
	Revision                  int64  `gorm:"not null;check:fleet_authority_revision,revision > 0"`
	Payload                   []byte `gorm:"not null"`
	PayloadSHA256             []byte `gorm:"not null;check:fleet_authority_payload_digest,octet_length(payload_sha256) = 32"`
	AcknowledgedRevision      int64  `gorm:"not null;default:0;check:fleet_authority_ack,acknowledged_revision >= 0 AND acknowledged_revision <= revision"`
	AcknowledgedSHA256        []byte
	DeliveryBlocked           bool `gorm:"not null;default:false"`
}
