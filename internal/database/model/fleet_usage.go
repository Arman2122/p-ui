package model

type FleetTermUsage struct {
	TermID            string `gorm:"type:uuid;primaryKey"`
	Uploaded          int64  `gorm:"not null;default:0;check:fleet_term_usage_up,uploaded >= 0"`
	Downloaded        int64  `gorm:"not null;default:0;check:fleet_term_usage_down,downloaded >= 0 AND uploaded <= 9223372036854775807 - downloaded"`
	ObservedUnixMilli int64  `gorm:"not null;default:0"`
	ReceivedUnixMilli int64  `gorm:"not null;default:0"`
}

type FleetTermUsageDay struct {
	TermID     string `gorm:"type:uuid;primaryKey"`
	Day        int64  `gorm:"primaryKey;index;check:fleet_term_usage_day,day >= 0"`
	Uploaded   int64  `gorm:"not null;default:0;check:fleet_term_day_up,uploaded >= 0"`
	Downloaded int64  `gorm:"not null;default:0;check:fleet_term_day_down,downloaded >= 0 AND uploaded <= 9223372036854775807 - downloaded"`
}
