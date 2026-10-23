package service

import (
	"encoding/json"
	"math"
	"slices"
)

func applyFleetNetworkOperations(current FleetNetworkOverrides, operations []FleetNetworkOperation) (FleetNetworkOverrides, error) {
	var result FleetNetworkOverrides
	data, err := json.Marshal(current)
	if err != nil || json.Unmarshal(data, &result) != nil || len(operations) > 8192 {
		return result, ErrFleetNetwork
	}
	if result.Bases == nil {
		result.Bases = map[string]FleetRuntimeBase{}
	}
	if result.Bridges == nil {
		result.Bridges = map[string]FleetNetworkBridge{}
	}
	if result.Inbounds == nil {
		result.Inbounds = map[int]FleetInboundNetwork{}
	}
	if result.AssetSlots == nil {
		result.AssetSlots = map[string]string{}
	}
	if result.AssetBindings == nil {
		result.AssetBindings = map[string]FleetAssetReference{}
	}
	for _, operation := range operations {
		if operation.Section != "inbound" && operation.InboundID != 0 {
			return result, ErrFleetNetwork
		}
		if !slices.Contains([]string{"replace", "reset", "remove"}, operation.Operation) {
			return result, ErrFleetNetwork
		}
		payloads := 0
		for _, present := range []bool{operation.Base != nil, operation.Bridge != nil, operation.Inbound != nil, operation.AssetKind != nil, operation.Asset != nil, operation.NativeNetwork != nil, operation.SSHHostKeys != nil} {
			if present {
				payloads++
			}
		}
		if operation.Operation == "replace" && payloads != 1 || operation.Operation != "replace" && payloads != 0 {
			return result, ErrFleetNetwork
		}
		if operation.Section != "native-network" && operation.Section != "ssh-host-keys" && operation.Section != "inbound" && !fleetNetworkName.MatchString(operation.Key) {
			return result, ErrFleetNetwork
		}
		replace := operation.Operation == "replace"
		switch operation.Section {
		case "base":
			delete(result.Bases, operation.Key)
			result.RemovedBases = slices.DeleteFunc(result.RemovedBases, func(key string) bool { return key == operation.Key })
			if replace {
				if operation.Base == nil {
					return result, ErrFleetNetwork
				}
				result.Bases[operation.Key] = *operation.Base
				if len(operation.Base.Providers) != 0 {
					result.Version = 2
				}
			}
			if operation.Operation == "remove" {
				result.RemovedBases = append(result.RemovedBases, operation.Key)
			}
		case "bridge":
			delete(result.Bridges, operation.Key)
			result.RemovedBridges = slices.DeleteFunc(result.RemovedBridges, func(key string) bool { return key == operation.Key })
			if replace {
				if operation.Bridge == nil {
					return result, ErrFleetNetwork
				}
				result.Bridges[operation.Key] = *operation.Bridge
			}
			if operation.Operation == "remove" {
				result.RemovedBridges = append(result.RemovedBridges, operation.Key)
			}
		case "asset-slot":
			delete(result.AssetSlots, operation.Key)
			result.RemovedAssetSlots = slices.DeleteFunc(result.RemovedAssetSlots, func(key string) bool { return key == operation.Key })
			if replace {
				if operation.AssetKind == nil || !validFleetAssetKind(*operation.AssetKind) {
					return result, ErrFleetNetwork
				}
				result.AssetSlots[operation.Key] = *operation.AssetKind
			}
			if operation.Operation == "remove" {
				result.RemovedAssetSlots = append(result.RemovedAssetSlots, operation.Key)
			}
		case "inbound":
			if operation.InboundID <= 0 || operation.Key != "" || operation.Operation == "remove" {
				return result, ErrFleetNetwork
			}
			delete(result.Inbounds, operation.InboundID)
			if replace {
				if operation.Inbound == nil {
					return result, ErrFleetNetwork
				}
				result.Inbounds[operation.InboundID] = *operation.Inbound
			}
		case "asset-binding":
			if operation.Operation == "remove" {
				return result, ErrFleetNetwork
			}
			delete(result.AssetBindings, operation.Key)
			if replace {
				if operation.Asset == nil || !fleetID(operation.Asset.AssetID) || operation.Asset.Revision == 0 || operation.Asset.Revision > math.MaxInt64 {
					return result, ErrFleetNetwork
				}
				result.AssetBindings[operation.Key] = *operation.Asset
			}
		case "native-network":
			if operation.Operation == "remove" || operation.Key != "" {
				return result, ErrFleetNetwork
			}
			result.NativeNetwork = nil
			if replace {
				if operation.NativeNetwork == nil {
					return result, ErrFleetNetwork
				}
				result.NativeNetwork = operation.NativeNetwork
			}
		case "ssh-host-keys":
			if operation.Operation == "remove" || operation.Key != "" {
				return result, ErrFleetNetwork
			}
			result.SSHHostKeys = nil
			if replace {
				if operation.SSHHostKeys == nil {
					return result, ErrFleetNetwork
				}
				result.SSHHostKeys = operation.SSHHostKeys
			}
		default:
			return result, ErrFleetNetwork
		}
	}
	slices.Sort(result.RemovedBases)
	slices.Sort(result.RemovedBridges)
	slices.Sort(result.RemovedAssetSlots)
	return result, nil
}
