package service

type ResellerSuspendRequest struct {
	Suspended *bool `json:"suspended" example:"true"`
}

type ResellerTopUpRequest struct {
	Traffic int64 `json:"traffic" example:"107374182400"`
	Days    int   `json:"days" example:"30"`
	Clients int   `json:"clients" example:"10"`
}

type ResellerTokenRequest struct {
	Name      string `json:"name" example:"Automation"`
	ExpiresAt int64  `json:"expiresAt" example:"1800000000000"`
}

type ResellerPasswordRequest struct {
	Current  string `json:"current" example:"current-account-password"`
	Password string `json:"password" example:"new-unique-account-password"`
}
