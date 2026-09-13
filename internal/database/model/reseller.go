package model

type ResellerAccount struct {
	UserID       int    `json:"userId" gorm:"primaryKey;autoIncrement:false" example:"2"`
	Name         string `json:"name" example:"Sina M."`
	Email        string `json:"email" example:"sina@example.com"`
	TrafficLimit int64  `json:"trafficLimit" example:"536870912000"`
	// Projected from the allocation ledger; the old column stays for schema compatibility.
	TrafficAllocated  int64 `json:"trafficAllocated" gorm:"->" example:"107374182400"`
	TrafficUsed       int64 `json:"trafficUsed" gorm:"not null;default:0" example:"10737418240"`
	BudgetBlocked     bool  `json:"budgetBlocked" gorm:"not null;default:false" example:"false"`
	ClientCap         int   `json:"clientCap" example:"50"`
	ExpiresAt         int64 `json:"expiresAt" example:"1800000000000"`
	Suspended         bool  `json:"suspended" example:"false"`
	AllowPlans        bool  `json:"allowPlans" example:"true"`
	AllowReset        bool  `json:"allowReset" example:"true"`
	AllowExtend       bool  `json:"allowExtend" example:"false"`
	AllowSuspend      bool  `json:"allowSuspend" example:"true"`
	AllowTopUp        bool  `json:"allowTopUp" example:"true"`
	NotifyOnDepletion bool  `json:"notifyOnDepletion" example:"true"`
	// Threshold notices fire once: the stamp is cleared when the account leaves the band.
	TrafficWarnedAt int64  `json:"-" gorm:"not null;default:0"`
	ExpiryWarnedAt  int64  `json:"-" gorm:"not null;default:0"`
	Note            string `json:"note,omitempty" example:"Partner account"`
	CreatedAt       int64  `json:"createdAt" gorm:"autoCreateTime:milli" example:"1790000000000"`
	UpdatedAt       int64  `json:"updatedAt" gorm:"autoUpdateTime:milli" example:"1790000000000"`
}

type ResellerInboundGrant struct {
	UserID    int `gorm:"primaryKey;autoIncrement:false"`
	InboundID int `gorm:"primaryKey;autoIncrement:false"`
}

type ResellerPausedClient struct {
	UserID   int `gorm:"primaryKey;autoIncrement:false"`
	ClientID int `gorm:"primaryKey;autoIncrement:false"`
}

type ResellerToken struct {
	ID         int    `json:"id" gorm:"primaryKey" example:"1"`
	UserID     int    `json:"-" gorm:"index"`
	Name       string `json:"name" example:"Automation"`
	Hash       string `json:"-" gorm:"uniqueIndex"`
	LoginEpoch int64  `json:"-"`
	ExpiresAt  int64  `json:"expiresAt" example:"1800000000000"`
	CreatedAt  int64  `json:"createdAt" gorm:"autoCreateTime:milli" example:"1790000000000"`
}

type ResellerAllocation struct {
	ID        int `gorm:"primaryKey"`
	UserID    int `gorm:"index"`
	Bytes     int64
	CreatedAt int64 `gorm:"autoCreateTime:milli;index"`
}

type ResellerUsageSource struct {
	ClientID   int    `gorm:"primaryKey;autoIncrement:false"`
	Source     string `gorm:"primaryKey"`
	Up         int64
	Down       int64
	TotalBytes int64
}

type ResellerClientUsage struct {
	ClientID       int `gorm:"primaryKey;autoIncrement:false"`
	TotalBytes     int64
	SourceOffset   int64 `gorm:"not null;default:0"`
	LifetimeOffset int64 `gorm:"not null;default:0"`
}
