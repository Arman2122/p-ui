package model

type FleetOutboundProvider struct {
	ID                   string  `gorm:"type:uuid;primaryKey;index:idx_fleet_provider_ready,priority:2,where:refresh_enabled"`
	Revision             int64   `gorm:"not null;check:fleet_provider_revision,revision > 0"`
	Name                 string  `gorm:"size:128;not null"`
	CoreID               string  `gorm:"size:48;not null"`
	Scope                string  `gorm:"not null;check:fleet_provider_scope,(scope = 'shared' AND node_id IS NULL AND installation_id IS NULL) OR (scope = 'node' AND node_id IS NOT NULL AND installation_id IS NOT NULL)"`
	NodeID               *string `gorm:"type:uuid"`
	InstallationID       *string `gorm:"type:uuid"`
	Configuration        string  `json:"-" gorm:"type:jsonb;not null"`
	ConfigurationDigest  []byte  `json:"-" gorm:"not null;check:fleet_provider_configuration_digest,octet_length(configuration_digest) = 32"`
	CachedRevision       int64   `gorm:"not null;default:0;check:fleet_provider_cached_revision,cached_revision >= 0"`
	RefreshEnabled       bool    `gorm:"not null;default:false"`
	ReadyUnixMilli       int64   `gorm:"not null;default:0;check:fleet_provider_ready,ready_unix_milli >= 0;index:idx_fleet_provider_ready,priority:1,where:refresh_enabled"`
	LeaseID              *string `gorm:"type:uuid"`
	LeaseUntilUnixMilli  int64   `gorm:"not null;default:0;check:fleet_provider_lease,(lease_id IS NULL AND lease_until_unix_milli = 0) OR (lease_id IS NOT NULL AND lease_until_unix_milli > 0)"`
	LastAttemptUnixMilli int64   `gorm:"not null;default:0"`
	LastSuccessUnixMilli int64   `gorm:"not null;default:0"`
	Diagnostic           string  `gorm:"size:64;not null;default:''"`
	CreatedUnixMilli     int64   `gorm:"not null"`
	ActorID              int     `gorm:"not null"`
	FanoutRevision       int64   `gorm:"not null;default:0;check:fleet_provider_fanout_revision,fanout_revision >= 0"`
	FanoutAfterNode      *string `gorm:"type:uuid"`
	FanoutPending        bool    `gorm:"not null;default:false;index:idx_fleet_provider_fanout,where:fanout_pending"`
	FanoutReadyUnixMilli int64   `gorm:"not null;default:0"`
}

type FleetOutboundProviderRevision struct {
	ProviderID            string `gorm:"type:uuid;primaryKey"`
	Revision              int64  `gorm:"primaryKey;check:fleet_provider_cache_revision,revision > 0"`
	ConfigurationRevision int64  `gorm:"not null;check:fleet_provider_cache_configuration_revision,configuration_revision > 0"`
	ConfigurationDigest   []byte `json:"-" gorm:"not null;check:fleet_provider_cache_configuration_digest,octet_length(configuration_digest) = 32"`
	Outbounds             string `json:"-" gorm:"type:jsonb;not null;check:fleet_provider_outbounds_size,octet_length(outbounds::text) <= 8388608"`
	Identities            string `json:"-" gorm:"type:jsonb;not null;check:fleet_provider_identities_size,octet_length(identities::text) <= 8388608"`
	OutboundsDigest       []byte `gorm:"not null;check:fleet_provider_outbounds_digest,octet_length(outbounds_digest) = 32"`
	IdentitiesDigest      []byte `json:"-" gorm:"not null;check:fleet_provider_identities_digest,octet_length(identities_digest) = 32"`
	Digest                []byte `gorm:"not null;check:fleet_provider_cache_digest,octet_length(digest) = 32"`
	CreatedUnixMilli      int64  `gorm:"not null"`
}

// Target-side foreign keys would acquire node locks while refresh holds a provider lock.
type FleetProviderRollout struct {
	NodeID                 string  `gorm:"type:uuid;primaryKey;index:idx_fleet_provider_rollout_ready,priority:2,where:pending"`
	InstallationID         string  `gorm:"type:uuid;not null"`
	Revision               int64   `gorm:"not null;check:fleet_provider_rollout_revision,revision > 0"`
	OperationID            string  `gorm:"type:uuid;not null;uniqueIndex"`
	BaseCompilationID      string  `gorm:"type:uuid;not null"`
	NodeRevisionID         string  `gorm:"type:uuid;not null"`
	DefaultsRevisionID     string  `gorm:"type:uuid;not null"`
	BaseDeploymentRevision int64   `gorm:"not null;check:fleet_provider_rollout_base,base_deployment_revision > 0"`
	BaseDigest             []byte  `gorm:"not null;check:fleet_provider_rollout_base_digest,octet_length(base_digest) = 32"`
	SourceDigest           []byte  `gorm:"not null;check:fleet_provider_rollout_source_digest,octet_length(source_digest) = 32"`
	Targets                string  `gorm:"type:jsonb;not null;check:fleet_provider_rollout_targets_size,octet_length(targets::text) <= 1048576"`
	Pending                bool    `gorm:"not null;default:true"`
	ReadyUnixMilli         int64   `gorm:"not null;check:fleet_provider_rollout_ready,ready_unix_milli >= 0;index:idx_fleet_provider_rollout_ready,priority:1,where:pending"`
	LeaseID                *string `gorm:"type:uuid"`
	LeaseUntilUnixMilli    int64   `gorm:"not null;default:0;check:fleet_provider_rollout_lease,(lease_id IS NULL AND lease_until_unix_milli = 0) OR (lease_id IS NOT NULL AND lease_until_unix_milli > 0)"`
	PublishedRevision      int64   `gorm:"not null;default:0;check:fleet_provider_rollout_published,published_revision >= 0"`
	PublishedCompilationID *string `gorm:"type:uuid"`
	Diagnostic             string  `gorm:"size:64;not null;default:''"`
}

// Pending publication pins wake after a later APPLIED acknowledgement, without taking node locks.
type FleetPublishedProviderBinding struct {
	NodeID                string `gorm:"type:uuid;primaryKey;index:idx_fleet_provider_binding_source,priority:2"`
	InstallationID        string `gorm:"type:uuid;primaryKey"`
	DeploymentRevision    int64  `gorm:"primaryKey;check:fleet_provider_binding_deployment,deployment_revision > 0"`
	RuntimeID             string `gorm:"size:48;primaryKey"`
	ProviderID            string `gorm:"type:uuid;primaryKey;index:idx_fleet_provider_binding_source,priority:1"`
	CompilationID         string `gorm:"type:uuid;not null"`
	ProviderRevision      int64  `gorm:"not null;check:fleet_provider_binding_revision,provider_revision > 0"`
	ConfigurationRevision int64  `gorm:"not null;check:fleet_provider_binding_configuration,configuration_revision > 0"`
	ProviderDigest        []byte `gorm:"not null;check:fleet_provider_binding_digest,octet_length(provider_digest) = 32"`
	FollowLatest          bool   `gorm:"not null;default:false"`
	ScopeKnown            bool   `gorm:"not null;default:false"`
	Pending               bool   `gorm:"not null;default:true;index:idx_fleet_provider_binding_wake,priority:2,where:pending"`
	ReadyUnixMilli        int64  `gorm:"not null;default:0;index:idx_fleet_provider_binding_wake,priority:1,where:pending"`
	Diagnostic            string `gorm:"size:64;not null;default:''"`
}
