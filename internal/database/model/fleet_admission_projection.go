package model

type FleetPublishedRuntime struct {
	NodeID              string `gorm:"type:uuid;primaryKey"`
	Revision            int64  `gorm:"primaryKey;check:fleet_published_runtime_revision,revision > 0"`
	RuntimeID           string `gorm:"size:48;primaryKey"`
	CoreID              string `gorm:"size:48;not null"`
	DeploymentDigest    []byte `gorm:"not null;check:fleet_published_deployment_digest,octet_length(deployment_digest) = 32"`
	PayloadDigest       []byte `gorm:"not null;check:fleet_published_payload_digest,octet_length(payload_digest) = 32"`
	GrantScopesComplete bool   `gorm:"not null;default:false"`
}

type FleetPublishedClientAdmission struct {
	NodeID               string `gorm:"type:uuid;primaryKey"`
	Revision             int64  `gorm:"primaryKey;check:fleet_published_client_revision,revision > 0"`
	RuntimeID            string `gorm:"size:48;primaryKey"`
	ClientID             string `gorm:"type:uuid;primaryKey"`
	PlacementID          string `gorm:"type:uuid;not null"`
	CredentialGeneration int64  `gorm:"not null;check:fleet_published_client_credentials,credential_generation > 0"`
	PlacementRevision    int64  `gorm:"not null;check:fleet_published_client_placement,placement_revision > 0"`
	DescriptorDigest     []byte `gorm:"not null;check:fleet_published_client_digest,octet_length(descriptor_digest) = 32"`
	BaseGrant            []byte `json:"-" gorm:"type:jsonb"`
}

type FleetPublishedAdmissionBinding struct {
	NodeID            string `gorm:"type:uuid;primaryKey"`
	Revision          int64  `gorm:"primaryKey;check:fleet_published_binding_revision,revision > 0"`
	RuntimeID         string `gorm:"size:48;primaryKey"`
	ClientID          string `gorm:"type:uuid;primaryKey"`
	InboundInstanceID string `gorm:"type:uuid;primaryKey"`
	InboundTag        string `gorm:"size:128;primaryKey"`
}
