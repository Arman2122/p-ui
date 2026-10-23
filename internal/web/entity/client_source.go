package entity

import "github.com/Arman2122/p-ui/internal/database/model"

type PlanOverrides struct {
	Quota    *bool `json:"quota" example:"true"`
	Expiry   *bool `json:"expiry" example:"true"`
	LimitIP  *bool `json:"limitIp" example:"true"`
	Inbounds *bool `json:"inbounds" example:"true"`
}

type ExternalLinkInput struct {
	Kind   string `json:"kind"`
	Value  string `json:"value"`
	Remark string `json:"remark"`
}

type ClientCredentialBindingInput struct {
	InboundID int               `json:"inboundId" example:"1"`
	Values    map[string]string `json:"values"`
}

type ClientCreatePayload struct {
	ExpectedActorID    int                            `json:"expectedActorId,omitempty" example:"1"`
	OperationID        string                         `json:"operationId,omitempty" example:"11111111-1111-4111-8111-111111111111"`
	PolicyID           *int                           `json:"policyId,omitempty" example:"1"`
	PlanOverrides      *PlanOverrides                 `json:"planOverrides,omitempty"`
	Client             model.Client                   `json:"client"`
	InboundIds         []int                          `json:"inboundIds" example:"[1]"`
	ExternalLinks      []ExternalLinkInput            `json:"externalLinks,omitempty"`
	CredentialBindings []ClientCredentialBindingInput `json:"credentialBindings,omitempty"`
}

type SourceClientCredentialRequirement struct {
	InboundID int      `json:"inboundId" example:"1"`
	Fields    []string `json:"fields" example:"[\"tlsCryptV2\"]"`
}

type FleetClientCreateResult struct {
	ActorID       int                                 `json:"actorId" example:"1"`
	ClientID      string                              `json:"clientId" example:"22222222-2222-4222-8222-222222222222"`
	PanelClientID int                                 `json:"panelClientId" example:"1"`
	Revision      uint64                              `json:"revision" example:"1"`
	Pending       bool                                `json:"pending" example:"true"`
	NodeDerived   []SourceClientCredentialRequirement `json:"nodeDerived"`
	Replayed      bool                                `json:"-"`
}

type FleetClientCreateCapabilities struct {
	Supported bool `json:"supported" example:"true"`
	ActorID   int  `json:"actorId" example:"1"`
}
