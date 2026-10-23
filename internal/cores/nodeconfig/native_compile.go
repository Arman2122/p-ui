package nodeconfig

import (
	"encoding/json"
	"math"
	"net/netip"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/Arman2122/p-ui/internal/awg"
	"github.com/Arman2122/p-ui/internal/core"
	awgcore "github.com/Arman2122/p-ui/internal/cores/internal/amneziawg"
	"github.com/Arman2122/p-ui/internal/cores/internal/ocserv"
	"github.com/Arman2122/p-ui/internal/cores/internal/openvpn"
	wgcore "github.com/Arman2122/p-ui/internal/cores/internal/wireguard"
	nativeruntime "github.com/Arman2122/p-ui/internal/cores/noderuntime/native"
	"github.com/Arman2122/p-ui/internal/node/native"
	engine "github.com/Arman2122/p-ui/internal/wireguard"
)

func CompileNative(input Input) (Result, error) {
	r, err := newRenderer(input)
	if err != nil {
		return Result{}, err
	}
	network := native.DefaultNetworkBinding()
	if input.NativeNetwork != nil {
		network = *input.NativeNetwork
	}
	if network.Validate() != nil {
		return Result{}, ErrInvalid
	}
	config := nativeruntime.Configuration{Network: &network}
	var identities []string
	inbounds := slices.Clone(input.Inbounds)
	sort.Slice(inbounds, func(i, j int) bool {
		return inbounds[i].TargetID < inbounds[j].TargetID || inbounds[i].TargetID == inbounds[j].TargetID && inbounds[i].Instance.Kind < inbounds[j].Instance.Kind
	})
	for _, inbound := range inbounds {
		kind := inbound.Instance.Kind
		if kind != wgcore.Kind && kind != awgcore.Kind && kind != openvpn.Kind && kind != ocserv.Kind {
			return Result{}, issue(inbound, "", "kind", ErrUnsupported)
		}
		if !inbound.Instance.Enable {
			r.result.Omissions = append(r.result.Omissions, Omission{SourceID: inbound.Instance.ID, Reason: "inbound-disabled"})
			continue
		}
		if inbound.Egress.Mode != "" && inbound.Egress.Mode != EgressDirect || inbound.Egress.BridgeID != "" {
			return Result{}, issue(inbound, "", "egress", ErrUnsupported)
		}
		if !unusedObject(inbound.Instance.StreamSettings) || !unusedObject(inbound.Instance.Sniffing) {
			return Result{}, issue(inbound, "", "streamSettings/sniffing", ErrUnsupported)
		}
		if inbound.Instance.Port < 1024 || inbound.Instance.Port > 65535 {
			return Result{}, issue(inbound, "", "port", ErrUnsupported)
		}
		clients, err := r.activeClients(inbound)
		if err != nil {
			return Result{}, err
		}
		if len(clients) > native.MaxClasses {
			return Result{}, issue(inbound, "", "client capacity", ErrUnsupported)
		}
		beforeClaims := len(r.result.Assets)
		var fragment nativeruntime.Configuration
		fragment.Network = &network
		listen, protocol := netip.IPv6Unspecified(), "udp"
		switch kind {
		case wgcore.Kind, awgcore.Kind:
			wg, params, err := r.nativeWireGuard(inbound, clients)
			if err != nil {
				return Result{}, err
			}
			if kind == wgcore.Kind {
				fragment.WireGuard = []nativeruntime.WireGuard{wg}
			} else {
				fragment.AmneziaWG = []nativeruntime.AmneziaWG{{WireGuard: wg, Parameters: params}}
			}
		case openvpn.Kind:
			vpn, err := r.nativeOpenVPN(inbound, clients)
			if err != nil {
				return Result{}, err
			}
			fragment.OpenVPN = []nativeruntime.OpenVPN{vpn}
			listen, protocol = vpn.Listen, vpn.Proto
		case ocserv.Kind:
			vpn, err := r.nativeOpenConnect(inbound, clients)
			if err != nil {
				return Result{}, err
			}
			fragment.OpenConnect = []nativeruntime.OpenConnect{vpn}
			listen, protocol = vpn.Listen, "tcp"
		}
		var selected []string
		for _, client := range clients {
			selected = append(selected, client.ClientID)
		}
		if kind != ocserv.Kind || len(clients) != 0 {
			data, err := json.Marshal(fragment)
			if err != nil || nativeruntime.ValidateConfiguration(data, selected) != nil {
				return Result{}, issue(inbound, "", "native configuration", ErrInvalid)
			}
		}
		if len(clients) == 0 {
			r.result.Assets = r.result.Assets[:beforeClaims]
			r.result.Omissions = append(r.result.Omissions, Omission{SourceID: inbound.Instance.ID, Reason: "no-enabled-clients"})
			continue
		}
		config.WireGuard = append(config.WireGuard, fragment.WireGuard...)
		config.AmneziaWG = append(config.AmneziaWG, fragment.AmneziaWG...)
		config.OpenVPN = append(config.OpenVPN, fragment.OpenVPN...)
		config.OpenConnect = append(config.OpenConnect, fragment.OpenConnect...)
		identities = append(identities, selected...)
		claim := ListenerClaim{RuntimeID: "native", SourceID: inbound.Instance.ID, TargetID: inbound.TargetID, Protocol: protocol, Address: listen, Port: uint16(inbound.Instance.Port), DualStack: kind == wgcore.Kind || kind == awgcore.Kind, Role: "client"}
		r.result.Listeners = append(r.result.Listeners, claim)
		if kind == ocserv.Kind {
			claim.Protocol = "udp"
			r.result.Listeners = append(r.result.Listeners, claim)
		}
	}
	if len(identities) != 0 {
		sort.Strings(identities)
		identities = slices.Compact(identities)
		data, err := json.Marshal(config)
		if err != nil || nativeruntime.ValidateConfiguration(data, identities) != nil {
			return Result{}, ErrConflict
		}
		r.result.Runtimes = []RuntimeDefinition{{ID: "native", Core: "native", Configuration: data, ClientIDs: identities}}
	}
	return finishResult(r.result)
}

type wireGuardSettings struct {
	SecretKey string            `json:"secretKey"`
	Address   []string          `json:"address"`
	MTU       int               `json:"mtu"`
	FWMark    int               `json:"fwmark"`
	DNS       string            `json:"dns"`
	AWG       json.RawMessage   `json:"awg"`
	Clients   []json.RawMessage `json:"clients"`
}

func (r *renderer) nativeWireGuard(inbound Inbound, clients []Client) (nativeruntime.WireGuard, awg.Params, error) {
	var result nativeruntime.WireGuard
	var params awg.Params
	var settings wireGuardSettings
	if strictSettings(inbound.Instance.Settings, &settings) != nil || allowedAssetFields(inbound, "secretKey") != nil {
		return result, params, issue(inbound, "", "settings", ErrInvalid)
	}
	if settings.FWMark != 0 || inbound.Native.Subnet.IsValid() {
		return result, params, issue(inbound, "", "fwmark/subnet", ErrUnsupported)
	}
	if inbound.Instance.Listen != "" && inbound.Instance.Listen != "0.0.0.0" && inbound.Instance.Listen != "::" {
		return result, params, issue(inbound, "", "listen", ErrUnsupported)
	}
	if len(settings.AWG) != 0 && string(settings.AWG) != "null" {
		if inbound.Instance.Kind != awgcore.Kind || !knownAWGFields(settings.AWG) {
			return result, params, issue(inbound, "", "awg", ErrInvalid)
		}
	}
	if inbound.Instance.Kind == awgcore.Kind {
		var err error
		params, err = awgcore.ParamsOf(inbound.Instance)
		if err != nil || params.Validate() != nil {
			return result, params, issue(inbound, "", "awg", ErrInvalid)
		}
	}
	var device engine.Instance
	if device.ApplySettings(inbound.Instance.Settings) != nil {
		return result, params, issue(inbound, "", "settings", ErrInvalid)
	}
	key, err := r.asset(inbound, "native", "secretKey", AssetWireGuardKey, AssetEmbedded)
	if err != nil || !sameMaterial(device.PrivateKey, key.Data) {
		return result, params, issue(inbound, "", "secretKey", ErrDependency)
	}
	result = nativeruntime.WireGuard{ID: inbound.TargetID, Port: uint16(inbound.Instance.Port), PrivateKey: string(key.Data)}
	addresses := inbound.Native.Addresses
	if len(addresses) == 0 {
		for _, value := range device.Address {
			prefix, err := netip.ParsePrefix(value)
			if err != nil {
				return result, params, issue(inbound, "", "address", ErrInvalid)
			}
			addresses = append(addresses, prefix)
		}
	}
	for _, address := range addresses {
		result.Addresses = append(result.Addresses, netip.PrefixFrom(address.Addr(), address.Addr().BitLen()))
	}
	mtu := device.MTU
	if inbound.Native.MTU != 0 {
		mtu = int(inbound.Native.MTU)
	}
	if mtu < 0 || mtu > 65535 {
		return result, params, issue(inbound, "", "mtu", ErrInvalid)
	}
	result.MTU = uint16(mtu)
	for _, client := range clients {
		if client.Native.Subnet.IsValid() || client.Native.MaxSessions != 0 {
			return result, params, issue(inbound, client.ClientID, "native binding", ErrInvalid)
		}
		public, err := credentialText(client, core.CredPublicKey, true)
		if err != nil {
			return result, params, issue(inbound, client.ClientID, "publicKey", ErrInvalid)
		}
		shared, err := credentialText(client, core.CredPreSharedKey, false)
		if err != nil {
			return result, params, issue(inbound, client.ClientID, "preSharedKey", ErrInvalid)
		}
		addresses := slices.Clone(client.Native.Addresses)
		if len(addresses) == 0 {
			values, err := credentialAddresses(client.User.Credentials[core.CredAllowedIPs])
			if err != nil {
				return result, params, issue(inbound, client.ClientID, "allowedIPs", err)
			}
			addresses = values
		}
		keepalive, err := credentialKeepalive(client.User.Credentials["keepAlive"])
		if err != nil {
			return result, params, issue(inbound, client.ClientID, "keepAlive", err)
		}
		sort.Slice(addresses, func(i, j int) bool { return addresses[i].Less(addresses[j]) })
		result.Clients = append(result.Clients, nativeruntime.Peer{ClientID: client.ClientID, PublicKey: public, PreSharedKey: shared, Addresses: addresses, KeepAlive: keepalive})
	}
	sort.Slice(result.Addresses, func(i, j int) bool { return result.Addresses[i].Addr().Less(result.Addresses[j].Addr()) })
	return result, params, nil
}

func knownAWGFields(data []byte) bool {
	var fields map[string]json.RawMessage
	if json.Unmarshal(data, &fields) != nil {
		return false
	}
	known := map[string]bool{}
	typ := reflect.TypeFor[awg.Params]()
	for index := 0; index < typ.NumField(); index++ {
		name, _, _ := strings.Cut(typ.Field(index).Tag.Get("json"), ",")
		known[name] = true
	}
	for field := range fields {
		if !known[field] {
			return false
		}
	}
	return true
}

func credentialText(client Client, name string, required bool) (string, error) {
	value, exists := client.User.Credentials[name]
	if !exists || value == nil {
		if required {
			return "", ErrDependency
		}
		return "", nil
	}
	text, ok := value.(string)
	if !ok || required && text == "" {
		return "", ErrInvalid
	}
	return text, nil
}

func credentialAddresses(raw any) ([]netip.Addr, error) {
	var values []string
	switch value := raw.(type) {
	case []string:
		values = value
	case []any:
		for _, item := range value {
			text, ok := item.(string)
			if !ok {
				return nil, ErrInvalid
			}
			values = append(values, text)
		}
	default:
		return nil, ErrDependency
	}
	var result []netip.Addr
	for _, value := range values {
		prefix, err := netip.ParsePrefix(value)
		if err != nil {
			return nil, ErrInvalid
		}
		if !prefix.IsSingleIP() {
			return nil, ErrUnsupported
		}
		result = append(result, prefix.Addr())
	}
	return result, nil
}

func credentialKeepalive(raw any) (uint16, error) {
	var value int64
	switch number := raw.(type) {
	case nil:
		return 0, nil
	case int:
		value = int64(number)
	case int64:
		value = number
	case float64:
		if math.IsNaN(number) || math.IsInf(number, 0) || number != math.Trunc(number) || number < 0 || number > 65535 {
			return 0, ErrInvalid
		}
		value = int64(number)
	case json.Number:
		parsed, err := strconv.ParseInt(string(number), 10, 64)
		if err != nil {
			return 0, ErrInvalid
		}
		value = parsed
	default:
		return 0, ErrInvalid
	}
	if value < 0 || value > 65535 {
		return 0, ErrInvalid
	}
	return uint16(value), nil
}
