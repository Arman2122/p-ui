package model

type ActivityEvent struct {
	Id             int64  `json:"id" gorm:"primaryKey;autoIncrement" example:"42"`
	AudienceUserID int    `json:"-" gorm:"index;not null;default:0"`
	ActorUserID    int    `json:"actorUserId" example:"1"`
	Kind           string `json:"kind" gorm:"not null" example:"core.crash"`
	Level          string `json:"level" gorm:"not null" example:"error"`
	TitleKey       string `json:"titleKey" example:"events.coreCrash.title"`
	MessageKey     string `json:"messageKey" example:"events.coreCrash.body"`
	Params         string `json:"-" gorm:"type:jsonb;not null;default:'{}'"`
	CreatedAt      int64  `json:"createdAt" gorm:"index;autoCreateTime:milli" example:"1790000000000"`
}

type ActivityRead struct {
	UserID  int   `gorm:"primaryKey;autoIncrement:false"`
	EventID int64 `gorm:"primaryKey;autoIncrement:false"`
	ReadAt  int64 `gorm:"autoCreateTime:milli"`
}
