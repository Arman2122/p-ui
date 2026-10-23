package model

type FleetNetworkDefaultsHead struct {
	ID                int    `gorm:"primaryKey;check:fleet_network_defaults_singleton,id = 1"`
	Revision          int64  `gorm:"not null;check:fleet_network_defaults_revision,revision > 0"`
	CurrentRevisionID string `gorm:"type:uuid;not null"`
}

type FleetNodeNetworkHead struct {
	NodeID                 string  `gorm:"type:uuid;primaryKey"`
	InstallationID         string  `gorm:"type:uuid;not null"`
	Revision               int64   `gorm:"not null;check:fleet_node_network_revision,revision > 0"`
	CurrentRevisionID      string  `gorm:"type:uuid;not null"`
	FollowDefaults         bool    `gorm:"not null;index"`
	DesiredDefaultsID      string  `gorm:"type:uuid;not null"`
	Pending                bool    `gorm:"not null;default:true;index"`
	LatestCompilationID    *string `gorm:"type:uuid"`
	PublishedCompilationID *string `gorm:"type:uuid"`
}

type FleetNetworkRevision struct {
	ID                string  `gorm:"type:uuid;primaryKey"`
	Scope             string  `gorm:"not null;check:fleet_network_scope,(scope = 'defaults' AND node_id IS NULL AND installation_id IS NULL) OR (scope = 'node' AND node_id IS NOT NULL AND installation_id IS NOT NULL)"`
	NodeID            *string `gorm:"type:uuid;uniqueIndex:idx_fleet_network_node_revision,priority:1"`
	InstallationID    *string `gorm:"type:uuid;uniqueIndex:idx_fleet_network_node_revision,priority:2"`
	Revision          int64   `gorm:"not null;check:fleet_network_revision,revision > 0;uniqueIndex:idx_fleet_network_default_revision,where:scope = 'defaults';uniqueIndex:idx_fleet_network_node_revision,priority:3"`
	BasedOnDefaultsID *string `gorm:"type:uuid"`
	Specification     string  `json:"-" gorm:"type:jsonb;not null"`
	Digest            []byte  `gorm:"not null;check:fleet_network_digest,octet_length(digest) = 32"`
	CreatedUnixMilli  int64   `gorm:"not null"`
	ActorID           int     `gorm:"not null"`
}

type FleetNetworkAssetHead struct {
	AssetID        string `gorm:"type:uuid;primaryKey"`
	NodeID         string `gorm:"type:uuid;not null;index"`
	InstallationID string `gorm:"type:uuid;not null"`
	Revision       int64  `gorm:"not null;check:fleet_asset_head_revision,revision > 0"`
}

type FleetNetworkAssetRevision struct {
	AssetID          string `gorm:"type:uuid;primaryKey"`
	Revision         int64  `gorm:"primaryKey;check:fleet_asset_revision,revision > 0"`
	NodeID           string `gorm:"type:uuid;not null;index"`
	InstallationID   string `gorm:"type:uuid;not null"`
	Kind             string `gorm:"not null"`
	Data             []byte `json:"-" gorm:"not null;check:fleet_asset_size,octet_length(data) > 0 AND octet_length(data) <= 8388608"`
	Digest           []byte `gorm:"not null;check:fleet_asset_digest,octet_length(digest) = 32"`
	CreatedUnixMilli int64  `gorm:"not null"`
	ActorID          int    `gorm:"not null"`
}

type FleetNetworkCompilation struct {
	ID                 string `gorm:"type:uuid;primaryKey"`
	NodeID             string `gorm:"type:uuid;not null;index"`
	InstallationID     string `gorm:"type:uuid;not null"`
	NodeRevisionID     string `gorm:"type:uuid;not null"`
	DefaultsRevisionID string `gorm:"type:uuid;not null"`
	CompilerVersion    string `gorm:"not null"`
	Manifest           string `gorm:"type:jsonb;not null"`
	RuntimeDefinitions string `json:"-" gorm:"type:jsonb;not null"`
	Preview            string `gorm:"type:jsonb;not null"`
	Digest             []byte `gorm:"not null;check:fleet_compilation_digest,octet_length(digest) = 32"`
	Ready              bool   `gorm:"not null"`
	CreatedUnixMilli   int64  `gorm:"not null"`
	ActorID            int    `gorm:"not null"`
}

type FleetNetworkPublication struct {
	OperationID        string `gorm:"type:uuid;primaryKey"`
	CompilationID      string `gorm:"type:uuid;not null;index"`
	NodeID             string `gorm:"type:uuid;not null;index"`
	DeploymentRevision int64  `gorm:"not null;check:fleet_network_publication_revision,deployment_revision > 0"`
	CreatedUnixMilli   int64  `gorm:"not null"`
}
