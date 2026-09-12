package model

type ClientTrafficSample struct {
	Email string `json:"email" gorm:"primaryKey" example:"alice"`
	T     int64  `json:"t" gorm:"primaryKey;index" example:"1789227000"`
	Up    int64  `json:"up" example:"1048576"`
	Down  int64  `json:"down" example:"8388608"`
}

type ClientHistoryCursor struct {
	Email string `json:"email" gorm:"primaryKey" example:"alice"`
	Up    int64  `json:"up" example:"1048576"`
	Down  int64  `json:"down" example:"8388608"`
}
