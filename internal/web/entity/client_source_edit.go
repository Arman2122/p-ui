package entity

import "github.com/Arman2122/p-ui/internal/database/model"

type FleetClientSourceBinding struct {
	InboundID    int               `json:"inboundId" example:"1"`
	Values       map[string]string `json:"values"`
	FlowOverride string            `json:"flowOverride" example:"xtls-rprx-vision"`
	Scope        string            `json:"scope" example:"effective"`
}

type FleetClientSourceView struct {
	ActorID              int                        `json:"actorId" example:"1"`
	ClientID             string                     `json:"clientId" example:"22222222-2222-4222-8222-222222222222"`
	PanelClientID        int                        `json:"panelClientId" example:"1"`
	Revision             uint64                     `json:"revision" example:"1"`
	Client               model.Client               `json:"client"`
	PolicyID             *int                       `json:"policyId" example:"1"`
	PlanOverrides        *PlanOverrides             `json:"planOverrides"`
	ExplicitInboundIDs   []int                      `json:"explicitInboundIds" example:"[1]"`
	EffectiveInboundIDs  *[]int                     `json:"effectiveInboundIds" example:"[1,2]"`
	EffectiveScopeKnown  bool                       `json:"effectiveScopeKnown" example:"true"`
	DependencyDiagnostic string                     `json:"dependencyDiagnostic" example:""`
	CredentialBindings   []FleetClientSourceBinding `json:"credentialBindings"`
	ExternalLinks        []ExternalLinkInput        `json:"externalLinks"`
	Pending              bool                       `json:"pending" example:"true"`
}
