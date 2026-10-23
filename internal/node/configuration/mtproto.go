package configuration

type MTProtoListener struct {
	Bind           string   `json:"bind"`
	Accept         []string `json:"accept"`
	Public         bool     `json:"public"`
	TrustedProxies []string `json:"trustedProxies,omitempty"`
}

type MTProtoClient struct {
	Secret  string `json:"secret"`
	AdTag   string `json:"adTag,omitempty"`
	IPLimit int    `json:"ipLimit,omitempty"`
}

type MTProtoWEB struct {
	Bind        string `json:"bind"`
	Host        string `json:"host"`
	CoverURL    string `json:"coverUrl"`
	MaxSessions int    `json:"maxSessions,omitempty"`
	MaxStreams  int    `json:"maxStreams,omitempty"`
	MaxPayload  int    `json:"maxPayload,omitempty"`
	BatchBytes  int64  `json:"batchBytes,omitempty"`
	PublicPort  uint16 `json:"publicPort,omitempty"`
	ServerCert  string `json:"serverCert,omitempty"`
	ServerKey   string `json:"serverKey,omitempty"`
}

type MTProtoInbound struct {
	ID                     string                   `json:"id"`
	Listeners              []MTProtoListener        `json:"listeners"`
	Clients                map[string]MTProtoClient `json:"clients"`
	SOCKS5                 string                   `json:"socks5,omitempty"`
	PreferIP               string                   `json:"preferIP,omitempty"`
	FrontingHost           string                   `json:"frontingHost,omitempty"`
	FrontingPort           int                      `json:"frontingPort,omitempty"`
	FrontingProxyProtocol  bool                     `json:"frontingProxyProtocol,omitempty"`
	PublicIPv4             string                   `json:"publicIPv4,omitempty"`
	PublicIPv6             string                   `json:"publicIPv6,omitempty"`
	AdvertisedPort         int                      `json:"advertisedPort,omitempty"`
	AdTag                  string                   `json:"adTag,omitempty"`
	Concurrency            uint                     `json:"concurrency,omitempty"`
	ThrottleMaxConnections int                      `json:"throttleMaxConnections,omitempty"`
	Debug                  bool                     `json:"debug,omitempty"`
	WEB                    *MTProtoWEB              `json:"web,omitempty"`
}

type MTProto struct {
	Inbounds []MTProtoInbound `json:"inbounds"`
}
