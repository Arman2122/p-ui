package service

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/cores/nodeconfig"
	"github.com/Arman2122/p-ui/internal/database/model"
)

type FleetProviderBinding struct {
	ProviderID   string `json:"providerId"`
	Revision     uint64 `json:"revision"`
	FollowLatest bool   `json:"followLatest"`
}

type FleetNetworkProviderPin struct {
	RuntimeID             string `json:"runtimeId"`
	ProviderID            string `json:"providerId"`
	Revision              uint64 `json:"revision"`
	ConfigurationRevision uint64 `json:"configurationRevision"`
	SHA256                string `json:"sha256"`
	FollowLatest          bool   `json:"followLatest"`
}

func validateFleetProviderBindings(version uint32, base FleetRuntimeBase) error {
	if len(base.Providers) > 32 || len(base.Providers) != 0 && (version != 2 || base.Core != "xray") {
		return ErrFleetNetwork
	}
	seen := map[string]bool{}
	for _, binding := range base.Providers {
		if !fleetID(binding.ProviderID) || binding.Revision == 0 || binding.Revision > math.MaxInt64 || seen[binding.ProviderID] {
			return ErrFleetNetwork
		}
		seen[binding.ProviderID] = true
	}
	return nil
}

func prepareFleetNetworkProviders(tx *gorm.DB, state fleetNetworkState, input *nodeconfig.Input, manifest *FleetNetworkManifest) ([]FleetNetworkDiagnostic, error) {
	ids := []string{}
	for _, base := range state.Resolved.Bases {
		for _, binding := range base.Providers {
			ids = append(ids, binding.ProviderID)
		}
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if len(ids) == 0 {
		return nil, nil
	}
	manifest.Version = 2
	providers := map[string]model.FleetOutboundProvider{}
	for _, id := range ids {
		var source model.FleetOutboundProvider
		query := tx.Clauses(clause.Locking{Strength: "SHARE"}).Find(&source, "id = ?", id)
		if query.Error != nil {
			return nil, query.Error
		}
		if query.RowsAffected != 0 {
			providers[id] = source
		}
	}
	var issues []FleetNetworkDiagnostic
	for index := range input.Bases {
		base := &input.Bases[index]
		for _, binding := range state.Resolved.Bases[base.RuntimeID].Providers {
			source, exists := providers[binding.ProviderID]
			if !exists || source.CoreID != base.Core || source.Scope == "node" && (source.NodeID == nil || source.InstallationID == nil || *source.NodeID != state.Node.ID || *source.InstallationID != state.Node.InstallationID) {
				issues = append(issues, FleetNetworkDiagnostic{Code: "PROVIDER_SCOPE_UNAVAILABLE", RuntimeID: base.RuntimeID})
				continue
			}
			follow := binding.FollowLatest && (state.Overrides.FollowDefaults || state.Origins["base/"+base.RuntimeID] == "overridden")
			revision := int64(binding.Revision)
			if follow {
				revision = source.CachedRevision
			}
			if revision == 0 {
				issues = append(issues, FleetNetworkDiagnostic{Code: "PROVIDER_CACHE_REQUIRED", RuntimeID: base.RuntimeID})
				continue
			}
			cached, err := loadProviderCache(tx, source.ID, revision)
			if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, ErrFleetProviderFetch) {
				issues = append(issues, FleetNetworkDiagnostic{Code: "PROVIDER_CACHE_UNAVAILABLE", RuntimeID: base.RuntimeID})
				continue
			}
			if err != nil {
				return nil, err
			}
			if follow && cached.ConfigurationRevision != source.Revision {
				issues = append(issues, FleetNetworkDiagnostic{Code: "PROVIDER_CONFIGURATION_PENDING", RuntimeID: base.RuntimeID})
				continue
			}
			var outbounds []json.RawMessage
			if json.Unmarshal([]byte(cached.Outbounds), &outbounds) != nil || nodeconfig.ValidateXrayProviderOutbounds([]byte(cached.Outbounds)) != nil {
				issues = append(issues, FleetNetworkDiagnostic{Code: "PROVIDER_CACHE_UNQUALIFIED", RuntimeID: base.RuntimeID})
				continue
			}
			if len(outbounds) == 0 {
				issues = append(issues, FleetNetworkDiagnostic{Code: "PROVIDER_EMPTY", RuntimeID: base.RuntimeID})
				continue
			}
			var object map[string]json.RawMessage
			if json.Unmarshal(base.Configuration, &object) != nil || object == nil {
				return nil, ErrFleetNetwork
			}
			var existing []json.RawMessage
			for key, value := range object {
				if strings.EqualFold(key, "outbounds") {
					if key != "outbounds" || json.Unmarshal(value, &existing) != nil {
						return nil, ErrFleetNetwork
					}
				}
			}
			encoded, err := json.Marshal(append(existing, outbounds...))
			if err != nil {
				return nil, err
			}
			object["outbounds"] = encoded
			base.Configuration, _, err = canonicalFleetArtifact(object, 8<<20)
			if err != nil {
				return nil, ErrFleetNetwork
			}
			manifest.Providers = append(manifest.Providers, FleetNetworkProviderPin{
				RuntimeID: base.RuntimeID, ProviderID: source.ID, Revision: uint64(cached.Revision),
				ConfigurationRevision: uint64(cached.ConfigurationRevision), SHA256: hex.EncodeToString(cached.Digest), FollowLatest: follow,
			})
		}
	}
	return issues, nil
}
