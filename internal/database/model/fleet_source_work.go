package model

// Target foreign keys would acquire node locks while the source writer holds its client lock.
type FleetSourceWork struct {
	PlacementID         string `gorm:"type:uuid;primaryKey;index:idx_fleet_source_work_current_due,priority:5,where:pending"`
	ClientID            string `gorm:"type:uuid;not null;index"`
	NodeID              string `gorm:"type:uuid;not null;index:idx_fleet_source_work_current_due,priority:1,where:pending"`
	InstallationID      string `gorm:"type:uuid;not null;index:idx_fleet_source_work_current_due,priority:2,where:pending"`
	RuntimeID           string `gorm:"size:48;not null"`
	OperationID         string `gorm:"type:uuid;not null"`
	Revision            int64  `gorm:"not null;check:fleet_source_work_revision,revision > 0"`
	SourceRevision      int64  `gorm:"not null;check:fleet_source_work_source_revision,source_revision > 0"`
	Enabled             bool   `gorm:"not null"`
	RequiresStop        bool   `gorm:"not null;index:idx_fleet_source_work_current_due,priority:3,sort:desc,where:pending"`
	AppliedStopRevision int64  `gorm:"not null;default:0;check:fleet_source_work_stop,applied_stop_revision >= 0"`
	Pending             bool   `gorm:"not null"`
	Diagnostic          string `gorm:"size:64;not null;default:''"`
	QueuedUnixMilli     int64  `gorm:"not null"`
	AvailableUnixMilli  int64  `gorm:"not null;default:0;index:idx_fleet_source_work_current_due,priority:4,where:pending"`
}
