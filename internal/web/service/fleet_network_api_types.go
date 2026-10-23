package service

type FleetNetworkDefaultsRequest struct {
	ExpectedRevision uint64           `json:"expectedRevision" example:"0"`
	Specification    FleetNetworkSpec `json:"specification"`
}
