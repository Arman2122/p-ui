package nodeconfig

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/netip"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/idna"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/node/configuration"
)

type mtprotoSettings struct {
	FakeTLSDomain         string `json:"fakeTlsDomain"`
	ProxyProtocolListener bool   `json:"proxyProtocolListener"`
	Debug                 bool   `json:"debug"`
	DomainFronting        struct {
		IP            string `json:"ip"`
		Port          int    `json:"port"`
		ProxyProtocol bool   `json:"proxyProtocol"`
	} `json:"domainFronting"`
	PreferIP               string          `json:"preferIp"`
	ThrottleMaxConnections int             `json:"throttleMaxConnections"`
	RouteThroughXray       bool            `json:"routeThroughXray"`
	RouteXrayPort          int             `json:"routeXrayPort"`
	WebBackendEnable       bool            `json:"webBackendEnable"`
	WebBackendPort         int             `json:"webBackendPort"`
	WebEnable              bool            `json:"webEnable"`
	WebHost                string          `json:"webHost"`
	WebCoverDir            string          `json:"webCoverDir"`
	WebCoverUpstream       string          `json:"webCoverUpstream"`
	WebBindPort            int             `json:"webBindPort"`
	WebStatsPort           int             `json:"webStatsPort"`
	PublicIPv4             string          `json:"publicIpv4"`
	PublicIPv6             string          `json:"publicIpv6"`
	Clients                json.RawMessage `json:"clients"`
}

func CompileMTProto(input Input) (Result, error) {
	r, err := newRenderer(input)
	if err != nil {
		return Result{}, err
	}
	for _, base := range input.Bases {
		if base.Core == "mtproto" {
			return Result{}, ErrUnsupported
		}
	}
	var config configuration.MTProto
	var clientIDs []string
	tags := map[string]bool{}
	inbounds := slices.Clone(input.Inbounds)
	sort.Slice(inbounds, func(i, j int) bool { return inbounds[i].TargetTag < inbounds[j].TargetTag })
	for _, inbound := range inbounds {
		if inbound.Instance.Kind != "mtproto" {
			return Result{}, issue(inbound, "", "protocol", ErrUnsupported)
		}
		if !inbound.Instance.Enable {
			r.result.Omissions = append(r.result.Omissions, Omission{SourceID: inbound.Instance.ID, Reason: "inbound-disabled"})
			continue
		}
		if !compilerName.MatchString(inbound.TargetTag) || tags[inbound.TargetTag] || !unusedObject(inbound.Instance.StreamSettings) || !unusedObject(inbound.Instance.Sniffing) {
			return Result{}, issue(inbound, "", "configuration", ErrInvalid)
		}
		tags[inbound.TargetTag] = true
		var source mtprotoSettings
		if strictSettings(inbound.Instance.Settings, &source) != nil {
			return Result{}, issue(inbound, "", "settings", ErrInvalid)
		}
		if source.RouteThroughXray || inbound.Egress.Mode != "" && inbound.Egress.Mode != EgressDirect || inbound.Egress.BridgeID != "" {
			return Result{}, issue(inbound, "", "egress", ErrDependency)
		}
		if !slices.Contains([]string{"", "prefer-ipv4", "prefer-ipv6", "only-ipv4", "only-ipv6"}, source.PreferIP) || source.ThrottleMaxConnections < 0 || source.DomainFronting.Port < 0 || source.DomainFronting.Port > 65535 {
			return Result{}, issue(inbound, "", "settings", ErrInvalid)
		}
		for i, value := range []string{source.PublicIPv4, source.PublicIPv6} {
			if value != "" {
				ip, err := netip.ParseAddr(value)
				if err != nil || ip.Is4In6() || ip.Zone() != "" || ip.Is4() != (i == 0) {
					return Result{}, issue(inbound, "", "public-address", ErrInvalid)
				}
			}
		}
		address, err := literalListen(inbound.Instance.Listen)
		if err != nil || inbound.Instance.Port < 1 || inbound.Instance.Port > 65535 {
			return Result{}, issue(inbound, "", "listener", ErrInvalid)
		}
		listener := configuration.MTProtoListener{Bind: net.JoinHostPort(address.String(), strconv.Itoa(inbound.Instance.Port)), Accept: []string{"ee"}}
		if source.ProxyProtocolListener {
			if inbound.MTProto == nil || len(inbound.MTProto.TrustedProxies) == 0 {
				return Result{}, issue(inbound, "", "trusted-proxies", ErrDependency)
			}
			for _, prefix := range inbound.MTProto.TrustedProxies {
				if !prefix.IsValid() || prefix != prefix.Masked() || prefix.Bits() == 0 || prefix.Addr().Is4In6() {
					return Result{}, issue(inbound, "", "trusted-proxies", ErrInvalid)
				}
				listener.TrustedProxies = append(listener.TrustedProxies, prefix.String())
			}
			sort.Strings(listener.TrustedProxies)
		} else if inbound.MTProto != nil && len(inbound.MTProto.TrustedProxies) != 0 {
			return Result{}, issue(inbound, "", "trusted-proxies", ErrConflict)
		}
		definition := configuration.MTProtoInbound{ID: inbound.TargetTag, Listeners: []configuration.MTProtoListener{listener}, Clients: map[string]configuration.MTProtoClient{}, PreferIP: source.PreferIP, FrontingHost: source.DomainFronting.IP, FrontingPort: source.DomainFronting.Port, FrontingProxyProtocol: source.DomainFronting.ProxyProtocol, ThrottleMaxConnections: source.ThrottleMaxConnections, Debug: source.Debug, PublicIPv4: source.PublicIPv4, PublicIPv6: source.PublicIPv6}
		if err := allowedAssetFields(inbound, "webCertificate", "webPrivateKey"); err != nil {
			return Result{}, err
		}
		web, webClaim, err := r.mtprotoWEB(inbound, source)
		if err != nil {
			return Result{}, err
		}
		definition.WEB = web
		assigned, err := r.activeClients(inbound)
		if err != nil {
			return Result{}, err
		}
		if len(assigned) == 0 {
			r.result.Omissions = append(r.result.Omissions, Omission{SourceID: inbound.Instance.ID, Reason: "no-enabled-clients"})
			continue
		}
		if source.WebEnable && len(assigned) > 4096 {
			return Result{}, issue(inbound, "", "web-clients", ErrUnsupported)
		}
		keys := map[string]bool{}
		for _, client := range assigned {
			secret := core.CredString(client.User.Credentials, core.CredSecret)
			decoded, err := hex.DecodeString(secret)
			if err != nil {
				decoded, err = base64.RawURLEncoding.DecodeString(secret)
			}
			if err != nil || len(decoded) < 18 || decoded[0] != 0xee || bytes.Equal(decoded[1:17], make([]byte, 16)) {
				return Result{}, issue(inbound, client.ClientID, "secret", ErrInvalid)
			}
			key := hex.EncodeToString(decoded[1:17])
			if keys[key] {
				return Result{}, issue(inbound, client.ClientID, "secret", ErrConflict)
			}
			keys[key] = true
			adTag := core.CredString(client.User.Credentials, core.CredAdTag)
			if adTag != "" {
				tag, err := hex.DecodeString(adTag)
				if err != nil || len(tag) != 16 {
					return Result{}, issue(inbound, client.ClientID, "adTag", ErrInvalid)
				}
			}
			definition.Clients[client.ClientID] = configuration.MTProtoClient{Secret: secret, AdTag: adTag}
			clientIDs = append(clientIDs, client.ClientID)
		}
		if source.WebBackendEnable && !source.WebEnable {
			if source.WebBackendPort < 1024 || source.WebBackendPort > 65535 {
				return Result{}, issue(inbound, "", "web-backend-port", ErrDependency)
			}
			definition.Listeners = append(definition.Listeners, configuration.MTProtoListener{Bind: net.JoinHostPort("127.0.0.1", strconv.Itoa(source.WebBackendPort)), Accept: []string{"dd", "plain"}})
			r.result.Listeners = append(r.result.Listeners, ListenerClaim{RuntimeID: "mtproto", SourceID: inbound.Instance.ID, TargetID: inbound.TargetID, Protocol: "tcp", Address: netip.MustParseAddr("127.0.0.1"), Port: uint16(source.WebBackendPort), Role: "local-obfuscated-backend"})
		}
		config.Inbounds = append(config.Inbounds, definition)
		r.result.Listeners = append(r.result.Listeners, ListenerClaim{RuntimeID: "mtproto", SourceID: inbound.Instance.ID, TargetID: inbound.TargetID, Protocol: "tcp", Address: address, Port: uint16(inbound.Instance.Port), DualStack: address.IsUnspecified(), Role: "client"})
		if webClaim != nil {
			r.result.Listeners = append(r.result.Listeners, *webClaim)
		}
	}
	if len(config.Inbounds) == 0 {
		return finishResult(r.result)
	}
	data, err := json.Marshal(config)
	if err != nil {
		return Result{}, err
	}
	r.result.Runtimes = []RuntimeDefinition{{ID: "mtproto", Core: "mtproto", Configuration: data, ClientIDs: clientIDs}}
	return finishResult(r.result)
}

func (r *renderer) mtprotoWEB(inbound Inbound, source mtprotoSettings) (*configuration.MTProtoWEB, *ListenerClaim, error) {
	if !source.WebEnable {
		if len(inbound.Assets) != 0 || inbound.MTProto != nil && inbound.MTProto.WEB != nil {
			return nil, nil, issue(inbound, "", "web", ErrConflict)
		}
		return nil, nil, nil
	}
	if inbound.MTProto == nil || inbound.MTProto.WEB == nil || source.WebCoverDir != "" {
		return nil, nil, issue(inbound, "", "web-binding", ErrDependency)
	}
	binding := inbound.MTProto.WEB
	address, err := literalListen(binding.Listen)
	if err != nil || binding.Port == 0 || binding.PublicPort == 0 {
		return nil, nil, issue(inbound, "", "web-binding", ErrInvalid)
	}
	cover, err := url.Parse(source.WebCoverUpstream)
	if err != nil || cover.Scheme != "https" || cover.Hostname() == "" || cover.User != nil {
		return nil, nil, issue(inbound, "", "web-cover", ErrUnsupported)
	}
	certificate, err := r.asset(inbound, "mtproto", "webCertificate", AssetCertificate, AssetEmbedded)
	if err != nil {
		return nil, nil, err
	}
	key, err := r.asset(inbound, "mtproto", "webPrivateKey", AssetPrivateKey, AssetEmbedded)
	if err != nil {
		return nil, nil, err
	}
	host, err := canonicalWEBHost(source.WebHost)
	if err != nil {
		return nil, nil, issue(inbound, "", "web-host", ErrInvalid)
	}
	web := &configuration.MTProtoWEB{Bind: net.JoinHostPort(address.String(), strconv.Itoa(int(binding.Port))), PublicPort: binding.PublicPort, Host: host, CoverURL: source.WebCoverUpstream, ServerCert: string(certificate.Data), ServerKey: string(key.Data)}
	if _, err := web.Certificate(); err != nil {
		return nil, nil, issue(inbound, "", "web-certificate", ErrInvalid)
	}
	return web, &ListenerClaim{RuntimeID: "mtproto", SourceID: inbound.Instance.ID, TargetID: inbound.TargetID, Protocol: "tcp", Address: address, Port: binding.Port, DualStack: address.IsUnspecified(), Role: "web-client"}, nil
}

func canonicalWEBHost(raw string) (string, error) {
	if !utf8.ValidString(raw) || strings.ContainsAny(raw, ":/?#@") {
		return "", ErrInvalid
	}
	host, err := idna.Lookup.ToASCII(strings.TrimSpace(raw))
	host = strings.ToLower(host)
	if err != nil || len(host) > 253 || !strings.Contains(host, ".") {
		return "", ErrInvalid
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return "", ErrInvalid
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", ErrInvalid
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return "", ErrInvalid
			}
		}
	}
	return host, nil
}
