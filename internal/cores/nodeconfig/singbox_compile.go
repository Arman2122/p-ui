package nodeconfig

import (
	"encoding/json"
	"slices"
	"sort"

	"github.com/Arman2122/p-ui/internal/cores/internal/singbox"
)

func CompileSingBox(input Input) (Result, error) {
	r, err := newRenderer(input)
	if err != nil {
		return Result{}, err
	}
	base, err := streamBase(input, "singbox", "outbounds", "route")
	if err != nil {
		return Result{}, err
	}
	if base == nil {
		base = map[string]json.RawMessage{"outbounds": json.RawMessage(`[{"type":"direct","tag":"direct"}]`), "route": json.RawMessage(`{"final":"direct"}`)}
	}
	if err := plainSingBoxBase(base); err != nil {
		return Result{}, err
	}
	inbounds := slices.Clone(input.Inbounds)
	sort.Slice(inbounds, func(i, j int) bool { return inbounds[i].TargetTag < inbounds[j].TargetTag })
	var rendered []map[string]any
	var clientIDs []string
	tags := map[string]bool{}
	for _, inbound := range inbounds {
		kind := inbound.Instance.Kind
		if kind != singbox.KindTUIC && kind != singbox.KindAnyTLS {
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
		var source struct {
			singbox.Settings
			Clients json.RawMessage `json:"clients"`
		}
		if strictSettings(inbound.Instance.Settings, &source) != nil {
			return Result{}, issue(inbound, "", "settings", ErrInvalid)
		}
		settings, err := singbox.NodeSettings(kind, source.Settings)
		if err != nil {
			return Result{}, issue(inbound, "", "settings", ErrInvalid)
		}
		if settings.RouteThroughXray || inbound.Egress.Mode != "" && inbound.Egress.Mode != EgressDirect || inbound.Egress.BridgeID != "" {
			return Result{}, issue(inbound, "", "egress", ErrDependency)
		}
		if err := allowedAssetFields(inbound, "certificate", "privateKey"); err != nil {
			return Result{}, err
		}
		certificate, key, err := r.streamCertificate(inbound, "singbox")
		if err != nil {
			return Result{}, err
		}
		address, err := literalListen(inbound.Instance.Listen)
		if err != nil || inbound.Instance.Port < 1 || inbound.Instance.Port > 65535 {
			return Result{}, issue(inbound, "", "listener", ErrInvalid)
		}
		clients, err := r.activeClients(inbound)
		if err != nil {
			return Result{}, err
		}
		if len(clients) == 0 {
			r.result.Omissions = append(r.result.Omissions, Omission{SourceID: inbound.Instance.ID, Reason: "no-enabled-clients"})
			continue
		}
		users := make([]map[string]any, 0, len(clients))
		credentials := map[string]bool{}
		for _, client := range clients {
			user := client.User
			user.Email = client.ClientID
			entry, err := singbox.NodeClient(kind, user)
			if err != nil {
				return Result{}, issue(inbound, client.ClientID, "credentials", ErrInvalid)
			}
			credential := entry["password"].(string)
			if kind == singbox.KindTUIC {
				credential = entry["uuid"].(string)
			}
			if credentials[credential] {
				return Result{}, issue(inbound, client.ClientID, "credentials", ErrConflict)
			}
			credentials[credential] = true
			users = append(users, entry)
			clientIDs = append(clientIDs, client.ClientID)
		}
		tls := map[string]any{"enabled": true, "certificate": certificate, "key": key}
		if settings.ServerName != "" {
			tls["server_name"] = settings.ServerName
		}
		definition := map[string]any{"type": string(kind), "tag": inbound.TargetTag, "listen": address.String(), "listen_port": inbound.Instance.Port, "users": users, "tls": tls}
		network := "tcp"
		if kind == singbox.KindTUIC {
			network = "udp"
			tls["alpn"] = []string{"h3"}
			definition["congestion_control"] = settings.CongestionControl
			definition["zero_rtt_handshake"] = settings.ZeroRTTHandshake
		} else if len(settings.PaddingScheme) > 0 {
			definition["padding_scheme"] = settings.PaddingScheme
		}
		rendered = append(rendered, definition)
		r.result.Listeners = append(r.result.Listeners, ListenerClaim{RuntimeID: "singbox", SourceID: inbound.Instance.ID, TargetID: inbound.TargetID, Protocol: network, Address: address, Port: uint16(inbound.Instance.Port), DualStack: address.IsUnspecified(), Role: "client"})
	}
	if len(rendered) == 0 {
		return finishResult(r.result)
	}
	base["inbounds"], err = json.Marshal(rendered)
	if err != nil {
		return Result{}, err
	}
	data, err := json.Marshal(base)
	if err != nil {
		return Result{}, err
	}
	r.result.Runtimes = []RuntimeDefinition{{ID: "singbox", Core: "singbox", Configuration: data, ClientIDs: clientIDs}}
	return finishResult(r.result)
}

func plainSingBoxBase(base map[string]json.RawMessage) error {
	var parsed struct {
		Outbounds []struct {
			Type string `json:"type"`
			Tag  string `json:"tag"`
		} `json:"outbounds"`
		Route struct {
			Final string `json:"final"`
		} `json:"route"`
	}
	data, err := json.Marshal(base)
	if err != nil || strictSettings(string(data), &parsed) != nil {
		return ErrUnsupported
	}
	if len(parsed.Outbounds) != 1 || parsed.Outbounds[0].Type != "direct" || !compilerName.MatchString(parsed.Outbounds[0].Tag) || parsed.Route.Final != parsed.Outbounds[0].Tag {
		return ErrUnsupported
	}
	return nil
}
