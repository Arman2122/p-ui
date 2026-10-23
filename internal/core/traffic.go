package core

// ClientTraffic is the per-client usage row. It is keyed by email and shared by
// every inbound a client uses, which is what makes one quota span every core.
type ClientTraffic struct {
	Accounting *TrafficAccounting `json:"accounting,omitempty" gorm:"-"`
	Id         int                `json:"id" form:"id" gorm:"primaryKey;autoIncrement" example:"14825"`
	InboundId  int                `json:"inboundId" form:"inboundId" gorm:"index:idx_client_traffics_inbound" example:"1"`
	Enable     bool               `json:"enable" form:"enable" example:"true"`
	Email      string             `json:"email" form:"email" gorm:"unique" example:"user1"`
	UUID       string             `json:"uuid" form:"uuid" gorm:"-" example:"e18c9a96-71bf-48d4-933f-8b9a46d4290c"`
	SubId      string             `json:"subId" form:"subId" gorm:"-" example:"i7tvdpeffi0hvvf1"`
	Up         int64              `json:"up" form:"up" example:"1048576"`
	Down       int64              `json:"down" form:"down" example:"2097152"`
	ExpiryTime int64              `json:"expiryTime" form:"expiryTime" gorm:"index:idx_client_traffics_renew,priority:1" example:"1735689600000"`
	Total      int64              `json:"total" form:"total" example:"10737418240"`
	Reset      int                `json:"reset" form:"reset" gorm:"default:0;index:idx_client_traffics_renew,priority:2" example:"0"`
	// Nil exists only on legacy node payloads; persisted rows always advertise a generation.
	ResetCount *int64 `json:"resetCount" gorm:"not null;default:0" example:"0"`
	LastOnline int64  `json:"lastOnline" form:"lastOnline" gorm:"default:0" example:"1735680000000"`
}

type TrafficAccounting struct {
	Source             string `json:"source" example:"fleet"`
	ClientID           string `json:"clientId,omitempty" example:"22222222-2222-4222-8222-222222222222"`
	TermID             string `json:"termId" example:"33253fd2-c71a-43c6-84e5-6277e435e4d2"`
	State              string `json:"state" example:"active"`
	MeasuredUploaded   int64  `json:"measuredUploaded" example:"1048576"`
	MeasuredDownloaded int64  `json:"measuredDownloaded" example:"2097152"`
	OpeningUploaded    int64  `json:"openingUploaded" example:"0"`
	OpeningDownloaded  int64  `json:"openingDownloaded" example:"0"`
	ReservedBytes      *int64 `json:"reservedBytes" example:"8388608"`
	UncertainBytes     *int64 `json:"uncertainBytes"`
	ReportedUnixMilli  int64  `json:"reportedUnixMilli" example:"1791498431342"`
	ObservedUnixMilli  int64  `json:"observedUnixMilli" example:"1791498431342"`
	HistoryGranularity string `json:"historyGranularity" example:"day"`
	HistoryBasis       string `json:"historyBasis" example:"reported"`
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

// TagDelta is one collection interval's bytes for a tagged subject — an inbound
// of this core, or an outbound it meters. Deltas only, like TrafficDelta.
type TagDelta struct {
	Tag string
	// Outbound distinguishes egress from one of the core's own inbounds. The
	// panel bills the two to different tables.
	Outbound bool
	Up       int64
	Down     int64
}
