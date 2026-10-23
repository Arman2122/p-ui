package model

type FleetClient struct {
	ID                      string  `gorm:"type:uuid;primaryKey"`
	PanelClientID           *int    `gorm:"uniqueIndex"`
	Revision                int64   `gorm:"not null;check:fleet_client_revision,revision > 0"`
	CredentialGeneration    int64   `gorm:"not null;check:fleet_client_credentials,credential_generation > 0"`
	CredentialClaimsVersion int64   `gorm:"not null;default:0;check:fleet_client_claims_version,credential_claims_version >= 0"`
	CredentialDigest        []byte  `gorm:"not null;check:fleet_client_digest,octet_length(credential_digest) = 32"`
	CurrentTermID           *string `gorm:"type:uuid"`
	PendingTermID           *string `gorm:"type:uuid"`
	Owned                   bool    `gorm:"not null;default:false"`
	PlacementRevision       int64   `gorm:"not null;default:1;check:fleet_client_placement_revision,placement_revision > 0"`
	PlacementPreferences    string  `gorm:"type:jsonb;not null;default:'{}';index:idx_fleet_client_placement_preferences,type:gin"`
}

type FleetTerm struct {
	ID                   string  `gorm:"type:uuid;primaryKey"`
	ClientID             string  `gorm:"type:uuid;not null;uniqueIndex:idx_fleet_term_generation"`
	Generation           int64   `gorm:"not null;uniqueIndex:idx_fleet_term_generation;check:fleet_term_generation,generation > 0"`
	PredecessorID        *string `gorm:"type:uuid"`
	Kind                 string  `gorm:"not null;check:fleet_term_kind,kind IN ('activation','renewal','traffic_reset')"`
	State                string  `gorm:"not null;check:fleet_term_state,state IN ('pending','active','draining','closed')"`
	CredentialGeneration int64   `gorm:"not null;check:fleet_term_credentials,credential_generation > 0"`
	SourcePolicyDigest   []byte  `gorm:"not null;check:fleet_term_policy_digest,octet_length(source_policy_digest) = 32"`
	BudgetID             string  `gorm:"type:uuid;not null;uniqueIndex"`
	DurationMillis       int64   `gorm:"not null;check:fleet_term_duration,duration_millis >= 0"`
	StartedUnixMilli     int64   `gorm:"not null;check:fleet_term_started,started_unix_milli >= 0"`
	ExpiresUnixMilli     int64   `gorm:"not null;check:fleet_term_expires,expires_unix_milli >= 0"`
	Rates                string  `gorm:"type:jsonb;not null"`
	OpeningUploaded      int64   `gorm:"not null;default:0;check:fleet_term_opening_up,opening_uploaded >= 0"`
	OpeningDownloaded    int64   `gorm:"not null;default:0;check:fleet_term_opening_down,opening_downloaded >= 0 AND opening_uploaded <= 9223372036854775807 - opening_downloaded"`
}

type FleetGrantStop struct {
	GrantID            string `gorm:"type:uuid;primaryKey"`
	DeploymentRevision int64  `gorm:"not null;check:fleet_grant_stop_deployment,deployment_revision > 0"`
	RateRevision       int64  `gorm:"not null;check:fleet_grant_stop_rate,rate_revision > 0"`
}
