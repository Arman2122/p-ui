package model

type GroupQuota struct {
	Name       string `json:"name" gorm:"primaryKey" example:"Family"`
	QuotaBytes int64  `json:"quotaBytes" gorm:"not null;default:0" example:"107374182400"`
}

type GroupInbound struct {
	Name      string `json:"name" gorm:"primaryKey" example:"Family"`
	InboundID int    `json:"inboundId" gorm:"primaryKey;index" example:"1"`
}
