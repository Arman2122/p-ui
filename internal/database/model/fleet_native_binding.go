package model

type FleetNativeBinding struct {
	ID                string `gorm:"type:uuid;primaryKey"`
	NodeID            string `gorm:"type:uuid;not null;uniqueIndex:idx_fleet_native_binding_subject;index:idx_fleet_native_binding_node,priority:1"`
	InstallationID    string `gorm:"type:uuid;not null;uniqueIndex:idx_fleet_native_binding_subject;index:idx_fleet_native_binding_node,priority:2"`
	PlacementID       string `gorm:"type:uuid;not null;index"`
	ClientID          string `gorm:"type:uuid;not null;uniqueIndex:idx_fleet_native_binding_subject"`
	InboundInstanceID string `gorm:"type:uuid;not null;uniqueIndex:idx_fleet_native_binding_subject"`
	TargetID          uint32 `gorm:"not null"`
	PoolDigest        []byte `gorm:"not null;check:fleet_native_pool_digest,octet_length(pool_digest) = 32"`
	Prefix            string `gorm:"size:49;not null"`
	MaxSessions       uint16 `gorm:"not null"`
	PublishedRevision int64  `gorm:"not null;check:fleet_native_published,published_revision > 0"`
}
