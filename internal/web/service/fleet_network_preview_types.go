package service

type FleetNetworkDiagnostic struct {
	Code        string `json:"code"`
	PlacementID string `json:"placementId,omitempty"`
	ClientID    string `json:"clientId,omitempty"`
	RuntimeID   string `json:"runtimeId,omitempty"`
	InboundID   int    `json:"inboundId,omitempty"`
	AssetSlot   string `json:"assetSlot,omitempty"`
}

type FleetNetworkAuthorityIdentity struct {
	ClientID             string            `json:"clientId"`
	CredentialGeneration uint64            `json:"credentialGeneration"`
	PlacementID          string            `json:"placementId"`
	PlacementRevision    uint64            `json:"placementRevision"`
	RuntimeID            string            `json:"runtimeId"`
	InboundInstanceIDs   []string          `json:"inboundInstanceIds"`
	InboundTags          map[string]string `json:"inboundTags"`
	TermID               string            `json:"termId,omitempty"`
	State                string            `json:"state"`
}

type FleetNetworkPlacementPin struct {
	PlacementID          string `json:"placementId"`
	ClientID             string `json:"clientId"`
	Revision             uint64 `json:"revision"`
	CredentialGeneration uint64 `json:"credentialGeneration"`
	PolicySHA256         string `json:"policySha256"`
	DefinitionSHA256     string `json:"definitionSha256"`
	CredentialSHA256     string `json:"credentialSha256"`
	GraphSHA256          string `json:"graphSha256"`
	TermID               string `json:"termId,omitempty"`
	Owned                bool   `json:"owned"`
	Enabled              bool   `json:"enabled"`
}

type FleetNetworkAssetPin struct {
	Slot     string `json:"slot"`
	AssetID  string `json:"assetId"`
	Revision uint64 `json:"revision"`
	Kind     string `json:"kind"`
	SHA256   string `json:"sha256"`
}

type FleetNetworkManifest struct {
	Version                uint32                          `json:"version"`
	NodeID                 string                          `json:"nodeId"`
	InstallationID         string                          `json:"installationId"`
	NodeRevisionID         string                          `json:"nodeRevisionId"`
	DefaultsRevisionID     string                          `json:"defaultsRevisionId"`
	NodeRevision           uint64                          `json:"nodeRevision"`
	BaseDeploymentRevision uint64                          `json:"baseDeploymentRevision"`
	Placements             []FleetNetworkPlacementPin      `json:"placements"`
	Assets                 []FleetNetworkAssetPin          `json:"assets"`
	Identities             []FleetNetworkAuthorityIdentity `json:"identities"`
	NativeBindings         []FleetNativeBindingPin         `json:"nativeBindings,omitempty"`
	Providers              []FleetNetworkProviderPin       `json:"providers,omitempty"`
}

type FleetNetworkRuntimeView struct {
	ID      string `json:"id"`
	Core    string `json:"core"`
	SHA256  string `json:"sha256"`
	Clients int    `json:"clients"`
}

type FleetNetworkListenerView struct {
	RuntimeID string `json:"runtimeId"`
	InboundID int    `json:"inboundId"`
	Address   string `json:"address"`
	Protocol  string `json:"protocol"`
	Port      uint16 `json:"port"`
	Role      string `json:"role"`
}

type FleetNetworkPreview struct {
	ID             string                          `json:"id"`
	NodeID         string                          `json:"nodeId"`
	InstallationID string                          `json:"installationId"`
	NodeRevision   FleetNetworkRevisionView        `json:"nodeRevision"`
	Defaults       FleetNetworkRevisionView        `json:"defaults"`
	SHA256         string                          `json:"sha256"`
	Ready          bool                            `json:"ready"`
	Origins        map[string]string               `json:"origins"`
	Runtimes       []FleetNetworkRuntimeView       `json:"runtimes"`
	Listeners      []FleetNetworkListenerView      `json:"listeners"`
	Assets         []FleetNetworkAssetPin          `json:"assets"`
	Identities     []FleetNetworkAuthorityIdentity `json:"identities"`
	Diagnostics    []FleetNetworkDiagnostic        `json:"diagnostics"`
	NativeBindings []FleetNativeBindingPin         `json:"nativeBindings,omitempty"`
}
