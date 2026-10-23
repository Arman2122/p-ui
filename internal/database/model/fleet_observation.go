package model

type FleetNodeObservation struct {
	NodeID                  string `gorm:"type:uuid;primaryKey"`
	RuntimeGeneration       int64  `gorm:"not null;default:0"`
	ReceivedUnixMilli       int64  `gorm:"not null;default:0"`
	ObservedUnixMilli       int64  `gorm:"not null;default:0"`
	ReportedAppliedRevision int64  `gorm:"not null;default:0"`
	ConditionsJSON          string `gorm:"type:jsonb;not null;default:'[]'"`
	ResultGeneration        int64  `gorm:"not null;default:0"`
	ResultRevision          int64  `gorm:"not null;default:0"`
	ResultStage             int32  `gorm:"not null;default:0"`
	ResultCode              string `gorm:"size:64;not null;default:''"`
	ResultReceivedUnixMilli int64  `gorm:"not null;default:0"`
}
