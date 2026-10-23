package nodeconfig

import (
	"encoding/base64"
	"encoding/json"
	"slices"
	"sort"
	"strings"

	"github.com/Arman2122/p-ui/internal/core"
	xraycore "github.com/Arman2122/p-ui/internal/cores/internal/xray"
	xrayruntime "github.com/Arman2122/p-ui/internal/cores/noderuntime/xray"
)

const AssetRealityPrivateKey = "xray-reality-private-key"

func CompileXray(input Input) (Result, error) {
	r, err := newRenderer(input)
	if err != nil {
		return Result{}, err
	}
	base, err := streamBase(input, "xray", "outbounds", "routing", "policy")
	if err != nil {
		return Result{}, err
	}
	if base == nil {
		base = map[string]json.RawMessage{"outbounds": json.RawMessage(`[{"protocol":"freedom","tag":"direct"}]`)}
	}
	if err := xrayBaseReferences(base); err != nil {
		return Result{}, err
	}
	if err := resolveXraySelectors(base, input); err != nil {
		return Result{}, err
	}
	inbounds := slices.Clone(input.Inbounds)
	sort.Slice(inbounds, func(i, j int) bool { return inbounds[i].TargetTag < inbounds[j].TargetTag })
	var rendered []map[string]any
	var clients []string
	metadata := xrayruntime.Metadata{Version: 1}
	tags := map[string]bool{}
	for _, inbound := range inbounds {
		if !slices.Contains([]core.Kind{"vmess", "vless", "trojan", "shadowsocks", "hysteria", "http", "mixed"}, inbound.Instance.Kind) {
			return Result{}, issue(inbound, "", "protocol", ErrUnsupported)
		}
		if !inbound.Instance.Enable {
			r.result.Omissions = append(r.result.Omissions, Omission{SourceID: inbound.Instance.ID, Reason: "inbound-disabled"})
			continue
		}
		if !compilerName.MatchString(inbound.TargetTag) || tags[inbound.TargetTag] {
			return Result{}, issue(inbound, "", "tag", ErrConflict)
		}
		tags[inbound.TargetTag] = true
		if inbound.Egress.BridgeID != "" || inbound.Egress.Mode != "" && inbound.Egress.Mode != EgressDirect {
			return Result{}, issue(inbound, "", "egress", ErrDependency)
		}
		if inbound.Egress.Mode == EgressDirect && !plainXrayBase(base) {
			return Result{}, issue(inbound, "", "egress", ErrConflict)
		}
		address, err := literalListen(inbound.Instance.Listen)
		if err != nil || inbound.Instance.Port < 1 || inbound.Instance.Port > 65535 {
			return Result{}, issue(inbound, "", "listener", ErrInvalid)
		}
		settings, err := xraySettings(inbound)
		if err != nil {
			return Result{}, err
		}
		stream, network, err := r.xrayStream(inbound)
		if err != nil {
			return Result{}, err
		}
		assigned, err := r.activeClients(inbound)
		if err != nil {
			return Result{}, err
		}
		if len(assigned) == 0 {
			r.result.Omissions = append(r.result.Omissions, Omission{SourceID: inbound.Instance.ID, Reason: "no-enabled-clients"})
			continue
		}
		entries := make([]map[string]any, 0, len(assigned))
		seen := map[string]bool{}
		seenNames := map[string]bool{}
		for _, client := range assigned {
			entry, key, err := xrayClient(inbound, client, settings)
			if err != nil {
				return Result{}, err
			}
			if seen[key] {
				return Result{}, issue(inbound, client.ClientID, "credentials", ErrConflict)
			}
			seen[key] = true
			entries = append(entries, entry)
			clients = append(clients, client.ClientID)
			name, _ := entry["email"].(string)
			if xraycore.UsesAccountUsernames(inbound.Instance.Kind) {
				name = entry["user"].(string)
			}
			if name == "" || len(name) > 256 || seenNames[name] {
				return Result{}, issue(inbound, client.ClientID, "identity", ErrConflict)
			}
			seenNames[name] = true
			metadata.ClientIdentities = append(metadata.ClientIdentities, xrayruntime.ClientIdentity{InboundTag: inbound.TargetTag, Username: name, ClientID: client.ClientID})
		}
		if xraycore.UsesAccountUsernames(inbound.Instance.Kind) {
			settings["accounts"] = entries
			if inbound.Instance.Kind == "mixed" {
				settings["auth"] = "password"
			}
		} else {
			settings["clients"] = entries
		}
		definition := map[string]any{"tag": inbound.TargetTag, "listen": address.String(), "port": inbound.Instance.Port, "protocol": string(inbound.Instance.Kind), "settings": settings}
		if len(stream) != 0 {
			definition["streamSettings"] = stream
		}
		if !unusedObject(inbound.Instance.Sniffing) {
			var sniffing map[string]any
			if json.Unmarshal([]byte(inbound.Instance.Sniffing), &sniffing) != nil || sniffing == nil {
				return Result{}, issue(inbound, "", "sniffing", ErrInvalid)
			}
			definition["sniffing"] = sniffing
		}
		rendered = append(rendered, definition)
		protocols := []string{network}
		if inbound.Instance.Kind == "shadowsocks" || inbound.Instance.Kind == "mixed" {
			protocols = []string{"tcp", "udp"}
		}
		for _, protocol := range protocols {
			r.result.Listeners = append(r.result.Listeners, ListenerClaim{RuntimeID: "xray", SourceID: inbound.Instance.ID, TargetID: inbound.TargetID, Protocol: protocol, Address: address, Port: uint16(inbound.Instance.Port), DualStack: address.IsUnspecified(), Role: "client"})
		}
	}
	if len(rendered) == 0 {
		return finishResult(r.result)
	}
	base["inbounds"], err = json.Marshal(rendered)
	if err != nil {
		return Result{}, err
	}
	base["penhoon"], err = json.Marshal(metadata)
	if err != nil {
		return Result{}, err
	}
	base["log"] = json.RawMessage(`{"loglevel":"none"}`)
	data, err := json.Marshal(base)
	if err != nil {
		return Result{}, err
	}
	if err := new(xrayruntime.Runtime).Validate(data); err != nil {
		return Result{}, ErrInvalid
	}
	r.result.Runtimes = []RuntimeDefinition{{ID: "xray", Core: "xray", Configuration: data, ClientIDs: clients}}
	return finishResult(r.result)
}

func xraySettings(inbound Inbound) (map[string]any, error) {
	settings := map[string]any{}
	if inbound.Instance.Settings != "" && (json.Unmarshal([]byte(inbound.Instance.Settings), &settings) != nil || settings == nil) {
		return nil, issue(inbound, "", "settings", ErrInvalid)
	}
	for _, field := range []string{"clients", "users", "accounts", "peers"} {
		delete(settings, field)
	}
	if externalFileReference(settings) {
		return nil, issue(inbound, "", "settings", ErrDependency)
	}
	if fallback, ok := settings["fallbacks"].([]any); ok && len(fallback) != 0 {
		return nil, issue(inbound, "", "fallbacks", ErrDependency)
	}
	return settings, nil
}

func xrayClient(inbound Inbound, client Client, settings map[string]any) (map[string]any, string, error) {
	entry := map[string]any{"email": client.User.Email}
	field, key := "", ""
	switch inbound.Instance.Kind {
	case "vmess", "vless":
		field = "id"
		key = core.CredString(client.User.Credentials, core.CredUUID)
		if alias := core.CredString(client.User.Credentials, "id"); alias != "" {
			if key != "" && key != alias {
				return nil, "", issue(inbound, client.ClientID, "uuid", ErrConflict)
			}
			key = alias
		}
		if !stableID(key) {
			return nil, "", issue(inbound, client.ClientID, "uuid", ErrInvalid)
		}
		if flow := core.CredString(client.User.Credentials, "flow"); flow != "" {
			if inbound.Instance.Kind != "vless" || flow != "xtls-rprx-vision" {
				return nil, "", issue(inbound, client.ClientID, "flow", ErrUnsupported)
			}
			entry["flow"] = flow
		}
		if reverse := client.User.Credentials["reverse"]; reverse != nil {
			return nil, "", issue(inbound, client.ClientID, "reverse", ErrUnsupported)
		}
	case "trojan", "shadowsocks":
		field, key = "password", core.CredString(client.User.Credentials, core.CredPassword)
		if inbound.Instance.Kind == "shadowsocks" {
			method, _ := settings["method"].(string)
			if !strings.HasPrefix(method, "2022-") {
				entry["method"] = method
			}
		}
	case "hysteria":
		field, key = "auth", core.CredString(client.User.Credentials, core.CredAuth)
	case "http", "mixed":
		username := core.CredString(client.User.Credentials, core.CredUsername)
		if username == "" {
			username = client.User.Email
		}
		password := core.CredString(client.User.Credentials, core.CredPassword)
		if username == "" || len(username) > 256 || password == "" {
			return nil, "", issue(inbound, client.ClientID, "credentials", ErrInvalid)
		}
		return map[string]any{"user": username, "pass": password}, username, nil
	}
	if key == "" {
		return nil, "", issue(inbound, client.ClientID, "credentials", ErrDependency)
	}
	entry[field] = key
	return entry, key, nil
}

func (r *renderer) xrayStream(inbound Inbound) (map[string]any, string, error) {
	stream := map[string]any{}
	if inbound.Instance.StreamSettings != "" && (json.Unmarshal([]byte(inbound.Instance.StreamSettings), &stream) != nil || stream == nil) {
		return nil, "", issue(inbound, "", "streamSettings", ErrInvalid)
	}
	security, _ := stream["security"].(string)
	switch security {
	case "", "none":
		if len(inbound.Assets) != 0 {
			return nil, "", issue(inbound, "", "assets", ErrInvalid)
		}
	case "tls":
		if err := allowedAssetFields(inbound, "certificate", "privateKey"); err != nil {
			return nil, "", err
		}
		certificate, key, err := r.streamCertificate(inbound, "xray")
		if err != nil {
			return nil, "", err
		}
		tls, ok := stream["tlsSettings"].(map[string]any)
		if !ok {
			tls = map[string]any{}
		}
		if _, exists := tls["keyLog"]; exists {
			return nil, "", issue(inbound, "", "keyLog", ErrUnsupported)
		}
		tls["certificates"] = []map[string]any{{"certificate": certificate, "key": key}}
		stream["tlsSettings"] = tls
	case "reality":
		if err := allowedAssetFields(inbound, "realityPrivateKey"); err != nil {
			return nil, "", err
		}
		asset, err := r.asset(inbound, "xray", "realityPrivateKey", AssetRealityPrivateKey, AssetEmbedded)
		if err != nil {
			return nil, "", err
		}
		key := strings.TrimSpace(string(asset.Data))
		decoded, err := base64.RawURLEncoding.DecodeString(key)
		if err != nil || len(decoded) != 32 || key != base64.RawURLEncoding.EncodeToString(decoded) {
			return nil, "", issue(inbound, "", "realityPrivateKey", ErrInvalid)
		}
		reality, ok := stream["realitySettings"].(map[string]any)
		if !ok {
			return nil, "", issue(inbound, "", "realitySettings", ErrInvalid)
		}
		reality["privateKey"] = key
		stream["realitySettings"] = reality
	default:
		return nil, "", issue(inbound, "", "security", ErrUnsupported)
	}
	if externalFileReference(stream) {
		return nil, "", issue(inbound, "", "streamSettings", ErrDependency)
	}
	network, _ := stream["network"].(string)
	if !slices.Contains([]string{"", "tcp", "raw", "ws", "grpc", "httpupgrade", "xhttp", "hysteria"}, network) {
		return nil, "", issue(inbound, "", "transport", ErrUnsupported)
	}
	if network == "hysteria" {
		return stream, "udp", nil
	}
	return stream, "tcp", nil
}
