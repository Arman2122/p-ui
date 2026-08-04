package core

// ClientTraffic is the per-client usage row. It is keyed by email and shared by
// every inbound a client uses, which is what makes one quota span every core.
type ClientTraffic struct {
	Id         int    `json:"id" form:"id" gorm:"primaryKey;autoIncrement" example:"14825"`
	InboundId  int    `json:"inboundId" form:"inboundId" gorm:"index:idx_client_traffics_inbound" example:"1"`
	Enable     bool   `json:"enable" form:"enable" example:"true"`
	Email      string `json:"email" form:"email" gorm:"unique" example:"user1"`
	UUID       string `json:"uuid" form:"uuid" gorm:"-" example:"e18c9a96-71bf-48d4-933f-8b9a46d4290c"`
	SubId      string `json:"subId" form:"subId" gorm:"-" example:"i7tvdpeffi0hvvf1"`
	Up         int64  `json:"up" form:"up" example:"1048576"`
	Down       int64  `json:"down" form:"down" example:"2097152"`
	ExpiryTime int64  `json:"expiryTime" form:"expiryTime" gorm:"index:idx_client_traffics_renew,priority:1" example:"1735689600000"`
	Total      int64  `json:"total" form:"total" example:"10737418240"`
	Reset      int    `json:"reset" form:"reset" gorm:"default:0;index:idx_client_traffics_renew,priority:2" example:"0"`
	LastOnline int64  `json:"lastOnline" form:"lastOnline" gorm:"default:0" example:"1735680000000"`
}

// TableName pins the table across the move out of internal/xray. Every existing
// install already has client_traffics; GORM must not derive a new name.
func (ClientTraffic) TableName() string { return "client_traffics" }

// TrafficDelta is what a core reports for one subject over one collection
// interval: bytes accrued since the previous read, never a cumulative total.
type TrafficDelta struct {
	// Email identifies the client. Tag identifies the inbound; a core that
	// cannot attribute a delta to an inbound leaves it empty.
	Email string
	Tag   string
	Up    int64
	Down  int64
}
