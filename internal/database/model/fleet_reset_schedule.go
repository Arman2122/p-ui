package model

// Pending work references its immutable operation receipt, never an inferred inbound reset.
type FleetResetSchedule struct {
	ClientID                 string  `gorm:"type:uuid;primaryKey;index:idx_fleet_reset_ready,priority:2,where:ready_unix_milli > 0"`
	Revision                 int64   `gorm:"not null;check:fleet_reset_revision,revision > 0"`
	OverrideCycle            *string `gorm:"size:16;check:fleet_reset_override,override_cycle IS NULL OR override_cycle IN ('none','hourly','daily','weekly','monthly')"`
	EffectiveCycle           string  `gorm:"size:16;not null;check:fleet_reset_cycle,effective_cycle IN ('none','hourly','daily','weekly','monthly')"`
	BoundTermID              *string `gorm:"type:uuid"`
	AnchorUnixMilli          int64   `gorm:"not null;check:fleet_reset_anchor,anchor_unix_milli >= 0"`
	NextDueUnixMilli         int64   `gorm:"not null;check:fleet_reset_due,next_due_unix_milli >= 0"`
	ReadyUnixMilli           int64   `gorm:"not null;check:fleet_reset_ready,ready_unix_milli >= 0;index:idx_fleet_reset_ready,priority:1,where:ready_unix_milli > 0"`
	PendingOperationID       *string `gorm:"type:uuid;uniqueIndex"`
	LastCompletedOperationID *string `gorm:"type:uuid"`
	Diagnostic               string  `gorm:"size:64;not null;default:''"`
}
