package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/Arman2122/p-ui/internal/node/native"
)

var (
	ErrFleetNetwork           = errors.New("invalid fleet network specification")
	ErrFleetNetworkDependency = errors.New("fleet network dependency is unresolved")
	ErrFleetNetworkRevision   = errors.New("fleet network source revision changed")
)

var fleetNetworkName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

type FleetRuntimeBase struct {
	Core          string                 `json:"core"`
	Configuration json.RawMessage        `json:"configuration"`
	Assets        map[string]string      `json:"assets,omitempty"`
	Providers     []FleetProviderBinding `json:"providers,omitempty" example:"[]"`
}

type FleetInboundNetwork struct {
	TargetID       uint32               `json:"targetId,omitempty"`
	TargetTag      string               `json:"targetTag,omitempty"`
	Listen         *string              `json:"listen,omitempty"`
	Port           *uint16              `json:"port,omitempty"`
	Settings       *json.RawMessage     `json:"settings,omitempty"`
	StreamSettings *json.RawMessage     `json:"streamSettings,omitempty"`
	Sniffing       *json.RawMessage     `json:"sniffing,omitempty"`
	Addresses      []netip.Prefix       `json:"addresses,omitempty"`
	Subnet         netip.Prefix         `json:"subnet,omitempty"`
	MTU            uint16               `json:"mtu,omitempty"`
	Assets         map[string]string    `json:"assets,omitempty"`
	EgressMode     string               `json:"egressMode,omitempty"`
	BridgeID       string               `json:"bridgeId,omitempty"`
	MTProto        *FleetMTProtoBinding `json:"mtproto,omitempty"`
}

type FleetMTProtoBinding struct {
	TrustedProxies []netip.Prefix          `json:"trustedProxies,omitempty"`
	WEB            *FleetMTProtoWEBBinding `json:"web,omitempty"`
}

type FleetMTProtoWEBBinding struct {
	Listen     string `json:"listen"`
	Port       uint16 `json:"port"`
	PublicPort uint16 `json:"publicPort"`
}

type FleetNetworkBridge struct {
	RuntimeID string         `json:"runtimeId"`
	Core      string         `json:"core"`
	Endpoint  netip.AddrPort `json:"endpoint"`
	UDP       bool           `json:"udp"`
}

type FleetSSHHostKeys struct {
	GenerateLocally bool   `json:"generateLocally"`
	AssetSlot       string `json:"assetSlot,omitempty"`
}

type FleetNetworkSpec struct {
	Version       uint32                        `json:"version" example:"1"`
	Bases         map[string]FleetRuntimeBase   `json:"bases,omitempty"`
	Inbounds      map[int]FleetInboundNetwork   `json:"inbounds,omitempty"`
	Bridges       map[string]FleetNetworkBridge `json:"bridges,omitempty"`
	AssetSlots    map[string]string             `json:"assetSlots,omitempty"`
	NativeNetwork *native.NetworkBinding        `json:"nativeNetwork,omitempty"`
	SSHHostKeys   *FleetSSHHostKeys             `json:"sshHostKeys,omitempty"`
}

type FleetAssetReference struct {
	AssetID  string `json:"assetId"`
	Revision uint64 `json:"revision"`
}

type FleetNetworkOverrides struct {
	Version           uint32                         `json:"version" example:"1"`
	FollowDefaults    bool                           `json:"followDefaults"`
	PinnedDefaultsID  string                         `json:"pinnedDefaultsId,omitempty"`
	Bases             map[string]FleetRuntimeBase    `json:"bases,omitempty"`
	RemovedBases      []string                       `json:"removedBases,omitempty"`
	Inbounds          map[int]FleetInboundNetwork    `json:"inbounds,omitempty"`
	Bridges           map[string]FleetNetworkBridge  `json:"bridges,omitempty"`
	RemovedBridges    []string                       `json:"removedBridges,omitempty"`
	AssetSlots        map[string]string              `json:"assetSlots,omitempty"`
	RemovedAssetSlots []string                       `json:"removedAssetSlots,omitempty"`
	AssetBindings     map[string]FleetAssetReference `json:"assetBindings,omitempty"`
	NativeNetwork     *native.NetworkBinding         `json:"nativeNetwork,omitempty"`
	SSHHostKeys       *FleetSSHHostKeys              `json:"sshHostKeys,omitempty"`
}

type FleetNetworkOperation struct {
	Section       string                 `json:"section"`
	Key           string                 `json:"key,omitempty"`
	InboundID     int                    `json:"inboundId,omitempty"`
	Operation     string                 `json:"operation"`
	Base          *FleetRuntimeBase      `json:"base,omitempty"`
	Inbound       *FleetInboundNetwork   `json:"inbound,omitempty"`
	Bridge        *FleetNetworkBridge    `json:"bridge,omitempty"`
	AssetKind     *string                `json:"assetKind,omitempty"`
	Asset         *FleetAssetReference   `json:"asset,omitempty"`
	NativeNetwork *native.NetworkBinding `json:"nativeNetwork,omitempty"`
	SSHHostKeys   *FleetSSHHostKeys      `json:"sshHostKeys,omitempty"`
}

func canonicalFleetNetwork(value any) ([]byte, []byte, error) {
	return canonicalFleetArtifact(value, 2<<20)
}

func canonicalFleetArtifact(value any, maximum int) ([]byte, []byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil || len(encoded) > maximum {
		return nil, nil, ErrFleetNetwork
	}
	var object any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if decoder.Decode(&object) != nil {
		return nil, nil, ErrFleetNetwork
	}
	encoded, err = json.Marshal(object)
	if err != nil {
		return nil, nil, err
	}
	digest := sha256.Sum256(encoded)
	return encoded, digest[:], nil
}

func validFleetAssetKind(kind string) bool {
	return slices.Contains([]string{"tls-certificate", "tls-private-key", "ssh-host-keys", "openvpn-tls-crypt-v2-server", "wireguard-private-key"}, kind)
}

func fleetNetworkObject(raw json.RawMessage, forbidden []string) error {
	var object map[string]json.RawMessage
	if len(raw) == 0 || len(raw) > 1<<20 || json.Unmarshal(raw, &object) != nil || object == nil {
		return ErrFleetNetwork
	}
	for key := range object {
		if slices.ContainsFunc(forbidden, func(value string) bool { return strings.EqualFold(key, value) }) {
			return ErrFleetNetwork
		}
	}
	return nil
}

func validateFleetNetwork(spec FleetNetworkSpec) error {
	if spec.Version != 1 && spec.Version != 2 || len(spec.Bases) > 32 || len(spec.Inbounds) > 4096 || len(spec.Bridges) > 4096 || len(spec.AssetSlots) > 8192 {
		return ErrFleetNetwork
	}
	if spec.NativeNetwork != nil && spec.NativeNetwork.Validate() != nil {
		return ErrFleetNetwork
	}
	for slot, kind := range spec.AssetSlots {
		if !fleetNetworkName.MatchString(slot) || !validFleetAssetKind(kind) {
			return ErrFleetNetwork
		}
	}
	assets := func(refs map[string]string) error {
		for field, slot := range refs {
			if field == "" || len(field) > 128 || spec.AssetSlots[slot] == "" {
				return ErrFleetNetworkDependency
			}
		}
		return nil
	}
	for id, base := range spec.Bases {
		if err := validateFleetProviderBindings(spec.Version, base); err != nil {
			return err
		}
		if !fleetRuntimeName.MatchString(id) || !fleetRuntimeName.MatchString(base.Core) || len(base.Configuration) > 1<<20 {
			return ErrFleetNetwork
		}
		if err := fleetNetworkObject(base.Configuration, []string{"inbounds", "clients", "users", "grants", "limits", "quotabytes", "creditceiling", "quotaunlimited", "term", "expiresunixmilli"}); err != nil {
			return err
		}
		if err := assets(base.Assets); err != nil {
			return err
		}
	}
	for id, bridge := range spec.Bridges {
		base, exists := spec.Bases[bridge.RuntimeID]
		if !fleetNetworkName.MatchString(id) || !exists || base.Core != bridge.Core || !bridge.Endpoint.IsValid() || !bridge.Endpoint.Addr().IsLoopback() || bridge.Endpoint.Addr().Is4In6() || bridge.Endpoint.Addr().Zone() != "" || bridge.Endpoint.Port() < 1024 {
			return ErrFleetNetworkDependency
		}
	}
	for id, inbound := range spec.Inbounds {
		if binding := inbound.MTProto; binding != nil {
			if len(binding.TrustedProxies) > 256 {
				return ErrFleetNetwork
			}
			for _, prefix := range binding.TrustedProxies {
				if !prefix.IsValid() || prefix.Addr().Is4In6() || prefix.Addr().Zone() != "" {
					return ErrFleetNetwork
				}
			}
			if web := binding.WEB; web != nil {
				address, err := netip.ParseAddr(web.Listen)
				if err != nil || address.Is4In6() || address.IsMulticast() || address.Zone() != "" || web.Port == 0 || web.PublicPort == 0 {
					return ErrFleetNetwork
				}
			}
		}
		for _, raw := range []*json.RawMessage{inbound.Settings, inbound.StreamSettings, inbound.Sniffing} {
			if raw != nil {
				if err := fleetNetworkObject(*raw, []string{"clients", "users", "peers", "grants", "limits", "quotabytes", "creditceiling", "quotaunlimited", "term", "expiresunixmilli"}); err != nil {
					return err
				}
			}
		}
		if id <= 0 || inbound.TargetID > 999999999 || len(inbound.TargetTag) > 128 || strings.ContainsRune(inbound.TargetTag, 0) || inbound.Port != nil && *inbound.Port == 0 {
			return ErrFleetNetwork
		}
		if inbound.Listen != nil {
			address, err := netip.ParseAddr(*inbound.Listen)
			if *inbound.Listen != "" && (err != nil || address.Is4In6() || address.IsMulticast() || address.Zone() != "") {
				return ErrFleetNetwork
			}
		}
		for _, address := range inbound.Addresses {
			if !address.IsValid() || address.Addr().Is4In6() || address.Addr().Zone() != "" {
				return ErrFleetNetwork
			}
		}
		if err := assets(inbound.Assets); err != nil {
			return err
		}
		if inbound.EgressMode != "" && inbound.EgressMode != "direct" && inbound.EgressMode != "xray" {
			return ErrFleetNetwork
		}
		if inbound.EgressMode == "xray" {
			if _, ok := spec.Bridges[inbound.BridgeID]; !ok {
				return ErrFleetNetworkDependency
			}
		} else if inbound.BridgeID != "" {
			return ErrFleetNetwork
		}
	}
	if keys := spec.SSHHostKeys; keys != nil {
		if keys.GenerateLocally && keys.AssetSlot != "" || !keys.GenerateLocally && spec.AssetSlots[keys.AssetSlot] != "ssh-host-keys" {
			return ErrFleetNetworkDependency
		}
	}
	return nil
}

func resolveFleetNetwork(defaults FleetNetworkSpec, overrides FleetNetworkOverrides) (FleetNetworkSpec, map[string]string, error) {
	var result FleetNetworkSpec
	encoded, err := json.Marshal(defaults)
	if err != nil || json.Unmarshal(encoded, &result) != nil || overrides.Version != 1 && overrides.Version != 2 || validateFleetNetwork(defaults) != nil {
		return result, nil, ErrFleetNetwork
	}
	for _, base := range overrides.Bases {
		if err := validateFleetProviderBindings(overrides.Version, base); err != nil {
			return result, nil, err
		}
	}
	if overrides.Version == 2 {
		result.Version = 2
	}
	if result.Bases == nil {
		result.Bases = map[string]FleetRuntimeBase{}
	}
	if result.Inbounds == nil {
		result.Inbounds = map[int]FleetInboundNetwork{}
	}
	if result.Bridges == nil {
		result.Bridges = map[string]FleetNetworkBridge{}
	}
	if result.AssetSlots == nil {
		result.AssetSlots = map[string]string{}
	}
	origins := map[string]string{}
	for key := range result.Bases {
		origins["base/"+key] = "inherited"
	}
	for key := range result.Bridges {
		origins["bridge/"+key] = "inherited"
	}
	for key := range result.AssetSlots {
		origins["asset-slot/"+key] = "inherited"
	}
	for key := range result.Inbounds {
		origins["inbound/"+strconv.Itoa(key)] = "inherited"
	}
	for _, key := range overrides.RemovedBases {
		delete(result.Bases, key)
		origins["base/"+key] = "removed"
	}
	for _, key := range overrides.RemovedBridges {
		delete(result.Bridges, key)
		origins["bridge/"+key] = "removed"
	}
	for _, key := range overrides.RemovedAssetSlots {
		delete(result.AssetSlots, key)
		origins["asset-slot/"+key] = "removed"
	}
	for key, value := range overrides.Bases {
		result.Bases[key] = value
		origins["base/"+key] = "overridden"
	}
	for key, value := range overrides.Inbounds {
		result.Inbounds[key] = value
		origins["inbound/"+strconv.Itoa(key)] = "overridden"
	}
	for key, value := range overrides.Bridges {
		result.Bridges[key] = value
		origins["bridge/"+key] = "overridden"
	}
	for key, value := range overrides.AssetSlots {
		result.AssetSlots[key] = value
		origins["asset-slot/"+key] = "overridden"
	}
	if overrides.NativeNetwork != nil {
		result.NativeNetwork = overrides.NativeNetwork
		origins["native-network"] = "overridden"
	} else {
		origins["native-network"] = "inherited"
	}
	if overrides.SSHHostKeys != nil {
		result.SSHHostKeys = overrides.SSHHostKeys
		origins["ssh-host-keys"] = "overridden"
	} else {
		origins["ssh-host-keys"] = "inherited"
	}
	if result.SSHHostKeys == nil {
		result.SSHHostKeys = &FleetSSHHostKeys{GenerateLocally: true}
		origins["ssh-host-keys"] = "builtin"
	}
	if result.NativeNetwork == nil {
		value := native.DefaultNetworkBinding()
		result.NativeNetwork = &value
		origins["native-network"] = "builtin"
	}
	return result, origins, validateFleetNetwork(result)
}
