package model

type FleetTermGroup struct {
	TermID  string `gorm:"type:uuid;primaryKey"`
	GroupID *int   `gorm:"index"`
}
