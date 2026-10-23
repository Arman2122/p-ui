package service

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/cores"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/entity"
)

var ErrFleetSourceCredentials = errors.New("fleet source credentials require valid, explicitly scoped binding values")

const maxFleetSourceCredentialBytes = 32 << 20

func validateFleetCredentialStorage(rows []model.ClientCredential) error {
	remaining := maxFleetSourceCredentialBytes
	for _, row := range rows {
		if len(row.Value) > remaining {
			return ErrFleetSourceCredentials
		}
		remaining -= len(row.Value)
	}
	return nil
}

type fleetPreparedCredentials struct {
	Client      model.Client
	Bindings    map[int]map[string]string
	NodeDerived []entity.SourceClientCredentialRequirement
}

func prepareFleetSourceCredentials(input model.Client, inbounds []model.Inbound, bindings []entity.ClientCredentialBindingInput) (fleetPreparedCredentials, error) {
	result := fleetPreparedCredentials{Client: input, Bindings: make(map[int]map[string]string, len(inbounds)), NodeDerived: []entity.SourceClientCredentialRequirement{}}
	if len(inbounds) > 4096 || len(bindings) > len(inbounds) {
		return result, ErrFleetSourceCredentials
	}
	if result.Client.ID == "" {
		result.Client.ID = uuid.NewString()
	}
	if !fleetID(result.Client.ID) {
		return result, ErrFleetSourceCredentials
	}
	byID := make(map[int]map[string]string, len(bindings))
	for _, binding := range bindings {
		if binding.InboundID <= 0 || byID[binding.InboundID] != nil || binding.Values == nil || len(binding.Values) > len(core.ClientCredentialNames()) {
			return result, ErrFleetSourceCredentials
		}
		byID[binding.InboundID] = maps.Clone(binding.Values)
	}
	common := clientCredentialValues(&result.Client)
	used := make(map[string]bool)
	for _, inbound := range inbounds {
		kind := core.Kind(inbound.Protocol)
		authority, ok := cores.SourceClientCredentialAuthority(kind)
		if !ok || inbound.Id <= 0 || inbound.NodeID != nil || result.Bindings[inbound.Id] != nil {
			return result, ErrFleetSourceCredentials
		}
		declared := authority.SourceClientCredentials(kind)
		values := make(map[string]string, len(declared))
		for _, name := range declared {
			if !core.IsClientCredential(name) {
				return result, ErrFleetSourceCredentials
			}
			values[name], used[name] = common[name], true
		}
		for name, value := range byID[inbound.Id] {
			if !slices.Contains(declared, name) {
				return result, ErrFleetSourceCredentials
			}
			values[name] = value
		}
		for _, value := range values {
			if len(value) > 64<<10 || strings.IndexByte(value, 0) >= 0 {
				return result, ErrFleetSourceCredentials
			}
		}
		minted, err := authority.MintSourceClientCredentials(kind, inbound.Settings, maps.Clone(values))
		if err != nil {
			return result, fmt.Errorf("%w: inbound %d: %w", ErrFleetSourceCredentials, inbound.Id, err)
		}
		for name, value := range minted.Values {
			if !slices.Contains(declared, name) || values[name] != "" || value == "" || len(value) > 64<<10 {
				return result, ErrFleetSourceCredentials
			}
			values[name] = value
		}
		if err := authority.ValidateSourceClientCredentials(kind, inbound.Settings, result.Client.Email, values); err != nil {
			return result, fmt.Errorf("%w: inbound %d: %w", ErrFleetSourceCredentials, inbound.Id, err)
		}
		missing := slices.Clone(minted.NodeDerived)
		slices.Sort(missing)
		missing = slices.Compact(missing)
		for _, name := range missing {
			if !slices.Contains(declared, name) || values[name] != "" {
				return result, ErrFleetSourceCredentials
			}
		}
		if len(missing) != 0 {
			result.NodeDerived = append(result.NodeDerived, entity.SourceClientCredentialRequirement{InboundID: inbound.Id, Fields: missing})
		}
		result.Bindings[inbound.Id] = values
		delete(byID, inbound.Id)
	}
	if len(byID) != 0 {
		return result, ErrFleetSourceCredentials
	}
	for _, name := range []string{core.CredSecret, core.CredAdTag, core.CredUsername, core.CredTLSCryptV2} {
		if common[name] != "" && !used[name] {
			return result, ErrFleetSourceCredentials
		}
	}
	return result, nil
}

func sourceCredentialStoredOnClient(name string) bool {
	switch name {
	case core.CredUUID, core.CredPassword, core.CredAuth, core.CredSecurity, core.CredPrivateKey, core.CredPublicKey, core.CredPreSharedKey, core.CredAllowedIPs:
		return true
	default:
		return false
	}
}

func fleetSourceCredentialRows(clientID int, prepared fleetPreparedCredentials) []model.ClientCredential {
	common := clientCredentialValues(&prepared.Client)
	var rows []model.ClientCredential
	for inboundID, values := range prepared.Bindings {
		for name, value := range values {
			if value == common[name] && sourceCredentialStoredOnClient(name) || value == "" && common[name] == "" {
				continue
			}
			rows = append(rows, model.ClientCredential{ClientId: clientID, InboundId: inboundID, Key: name, Value: value})
		}
	}
	slices.SortFunc(rows, func(a, b model.ClientCredential) int {
		if a.InboundId < b.InboundId {
			return -1
		}
		if a.InboundId > b.InboundId {
			return 1
		}
		return strings.Compare(a.Key, b.Key)
	})
	return rows
}
