package entity

type FleetClientMetadataRequest struct {
	OperationID      string  `json:"operationId" example:"11111111-1111-4111-8111-111111111111"`
	ExpectedActorID  int     `json:"expectedActorId" example:"1"`
	ClientID         string  `json:"clientId" example:"22222222-2222-4222-8222-222222222222"`
	ExpectedRevision uint64  `json:"expectedRevision" example:"1"`
	Comment          *string `json:"comment" example:"Account note"`
}

type FleetClientMetadataResult struct {
	ActorID       int    `json:"actorId" example:"1"`
	ClientID      string `json:"clientId" example:"22222222-2222-4222-8222-222222222222"`
	PanelClientID int    `json:"panelClientId" example:"1"`
	Revision      uint64 `json:"revision" example:"2"`
	Changed       bool   `json:"changed" example:"true"`
	Replayed      bool   `json:"-"`
}
