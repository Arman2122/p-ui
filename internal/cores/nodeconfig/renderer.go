package nodeconfig

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/netip"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/node/configuration"
)

var compilerName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)

type renderer struct {
	input   Input
	result  Result
	assets  map[string]Asset
	bridges map[string]Bridge
}

func stableID(value string) bool {
	id, err := uuid.Parse(value)
	return err == nil && id != uuid.Nil && id.String() == value
}

func issue(inbound Inbound, client, field string, cause error) error {
	return fmt.Errorf("%w: inbound %d, client %s, field %s", cause, inbound.Instance.ID, client, field)
}

func newRenderer(input Input) (*renderer, error) {
	if !stableID(input.NodeID) || len(input.Inbounds) > 4096 || len(input.Assets) > 8192 || len(input.Bridges) > 4096 {
		return nil, ErrInvalid
	}
	r := &renderer{input: input, assets: map[string]Asset{}, bridges: map[string]Bridge{}}
	ids, targets := map[int]bool{}, map[string]bool{}
	totalClients := 0
	for _, inbound := range input.Inbounds {
		totalClients += len(inbound.Clients)
		key := fmt.Sprintf("%s/%d", inbound.Instance.Kind, inbound.TargetID)
		if inbound.Instance.ID < 1 || ids[inbound.Instance.ID] || inbound.TargetID < 1 || inbound.TargetID > 999999999 || targets[key] || len(inbound.Instance.Users) != 0 || totalClients > 1_000_000 || len(inbound.Instance.Settings) > 32<<20 {
			return nil, issue(inbound, "", "identity", ErrInvalid)
		}
		ids[inbound.Instance.ID], targets[key] = true, true
		clients := map[string]bool{}
		for _, client := range inbound.Clients {
			if !stableID(client.ClientID) || clients[client.ClientID] {
				return nil, issue(inbound, client.ClientID, "identity", ErrInvalid)
			}
			clients[client.ClientID] = true
		}
	}
	var total int
	for _, asset := range input.Assets {
		total += len(asset.Data)
		if !compilerName.MatchString(asset.ID) || asset.NodeID != input.NodeID || r.assets[asset.ID].ID != "" || len(asset.Data) == 0 || len(asset.Data) > 8<<20 || total > 32<<20 {
			return nil, ErrDependency
		}
		r.assets[asset.ID] = asset
	}
	for _, bridge := range input.Bridges {
		if !compilerName.MatchString(bridge.ID) || bridge.NodeID != input.NodeID || r.bridges[bridge.ID].ID != "" || !compilerName.MatchString(bridge.RuntimeID) || !compilerName.MatchString(bridge.Core) || !bridge.Endpoint.IsValid() || bridge.Endpoint.Port() < 1024 || !bridge.Endpoint.Addr().IsLoopback() || bridge.Endpoint.Addr().Is4In6() || bridge.Endpoint.Addr().Zone() != "" {
			return nil, ErrDependency
		}
		r.bridges[bridge.ID] = bridge
	}
	return r, nil
}

func strictSettings(raw string, value any) error {
	if strings.TrimSpace(raw) == "" {
		raw = "{}"
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil || decoder.Decode(new(any)) != io.EOF {
		return ErrInvalid
	}
	return nil
}

func unusedObject(raw string) bool {
	var object map[string]json.RawMessage
	return strings.TrimSpace(raw) == "" || json.Unmarshal([]byte(raw), &object) == nil && len(object) == 0
}

func (r *renderer) asset(inbound Inbound, runtime, field, kind, delivery string) (Asset, error) {
	id := inbound.Assets[field]
	asset, ok := r.assets[id]
	if !ok || asset.Kind != kind {
		return Asset{}, issue(inbound, "", field, ErrDependency)
	}
	digest := sha256.Sum256(asset.Data)
	claim := AssetClaim{AssetID: id, RuntimeID: runtime, SourceID: inbound.Instance.ID, Field: field, Kind: kind, Delivery: delivery, SHA256: hex.EncodeToString(digest[:])}
	if delivery == AssetFile {
		claim.NodePath = configuration.AssetReference(id, claim.SHA256)
	}
	r.result.Assets = append(r.result.Assets, claim)
	return asset, nil
}

func fileAsset(asset Asset) configuration.FileAsset {
	digest := sha256.Sum256(asset.Data)
	return configuration.FileAsset{ID: asset.ID, SHA256: hex.EncodeToString(digest[:]), Data: slices.Clone(asset.Data)}
}

func (r *renderer) fileAssets(runtime string) []configuration.FileAsset {
	seen := map[string]bool{}
	var files []configuration.FileAsset
	for _, claim := range r.result.Assets {
		if claim.RuntimeID == runtime && claim.Delivery == AssetFile && !seen[claim.AssetID] {
			files = append(files, fileAsset(r.assets[claim.AssetID]))
			seen[claim.AssetID] = true
		}
	}
	slices.SortFunc(files, func(a, b configuration.FileAsset) int { return strings.Compare(a.ID, b.ID) })
	return files
}

func allowedAssetFields(inbound Inbound, fields ...string) error {
	for field := range inbound.Assets {
		if !slices.Contains(fields, field) {
			return issue(inbound, "", "assets", ErrInvalid)
		}
	}
	return nil
}

func (r *renderer) activeClients(inbound Inbound) ([]Client, error) {
	var clients []Client
	for _, client := range inbound.Clients {
		if !client.User.Enable {
			r.result.Omissions = append(r.result.Omissions, Omission{SourceID: inbound.Instance.ID, ClientID: client.ClientID, Reason: "client-disabled"})
			continue
		}
		if client.User.LimitIP != 0 {
			return nil, issue(inbound, client.ClientID, "limitIP", ErrUnsupported)
		}
		clients = append(clients, client)
	}
	sort.Slice(clients, func(i, j int) bool { return clients[i].ClientID < clients[j].ClientID })
	return clients, nil
}

func literalListen(raw string) (netip.Addr, error) {
	if raw == "" {
		return netip.IPv6Unspecified(), nil
	}
	address, err := netip.ParseAddr(raw)
	if err != nil || address.Is4In6() || address.Zone() != "" || address.IsMulticast() {
		return netip.Addr{}, ErrInvalid
	}
	return address, nil
}

func listenerConflict(left, right ListenerClaim) bool {
	if left.Protocol != right.Protocol || left.Port != right.Port {
		return false
	}
	if left.Address == right.Address || left.DualStack && left.Address.IsUnspecified() || right.DualStack && right.Address.IsUnspecified() {
		return true
	}
	return left.Address.BitLen() == right.Address.BitLen() && (left.Address.IsUnspecified() || right.Address.IsUnspecified())
}

func finishResult(result Result) (Result, error) {
	sort.Slice(result.Runtimes, func(i, j int) bool { return result.Runtimes[i].ID < result.Runtimes[j].ID })
	payloadBytes, fileBytes := 0, 0
	for i := range result.Runtimes {
		if configuration.ValidateFileAssets(result.Runtimes[i].FileAssets) != nil {
			return Result{}, ErrInvalid
		}
		for _, asset := range result.Runtimes[i].FileAssets {
			fileBytes += len(asset.Data)
		}
		payloadBytes += len(result.Runtimes[i].Configuration)
		if payloadBytes > 32<<20 || fileBytes > configuration.MaxBundleFileBytes {
			return Result{}, ErrInvalid
		}
		sort.Strings(result.Runtimes[i].ClientIDs)
		result.Runtimes[i].ClientIDs = slices.Compact(result.Runtimes[i].ClientIDs)
		if i > 0 && result.Runtimes[i-1].ID == result.Runtimes[i].ID {
			return Result{}, ErrConflict
		}
	}
	sort.Slice(result.Listeners, func(i, j int) bool {
		a, b := result.Listeners[i], result.Listeners[j]
		return fmt.Sprintf("%s/%05d/%s/%s/%d", a.Protocol, a.Port, a.Address, a.RuntimeID, a.TargetID) < fmt.Sprintf("%s/%05d/%s/%s/%d", b.Protocol, b.Port, b.Address, b.RuntimeID, b.TargetID)
	})
	groupStart := 0
	for i, claim := range result.Listeners {
		if i > 0 && (result.Listeners[i-1].Protocol != claim.Protocol || result.Listeners[i-1].Port != claim.Port) {
			groupStart = i
		}
		for _, previous := range result.Listeners[groupStart:i] {
			if listenerConflict(claim, previous) {
				return Result{}, fmt.Errorf("%w: listener %s/%d", ErrConflict, claim.Protocol, claim.Port)
			}
		}
	}
	sort.Slice(result.Assets, func(i, j int) bool {
		a, b := result.Assets[i], result.Assets[j]
		return fmt.Sprintf("%s/%d/%s/%s", a.RuntimeID, a.SourceID, a.Field, a.AssetID) < fmt.Sprintf("%s/%d/%s/%s", b.RuntimeID, b.SourceID, b.Field, b.AssetID)
	})
	sort.Slice(result.Bridges, func(i, j int) bool {
		a, b := result.Bridges[i], result.Bridges[j]
		return fmt.Sprintf("%s/%s/%d", a.ID, a.ConsumerRuntimeID, a.SourceID) < fmt.Sprintf("%s/%s/%d", b.ID, b.ConsumerRuntimeID, b.SourceID)
	})
	sort.Slice(result.Omissions, func(i, j int) bool {
		a, b := result.Omissions[i], result.Omissions[j]
		return fmt.Sprintf("%012d/%s/%s", a.SourceID, a.ClientID, a.Reason) < fmt.Sprintf("%012d/%s/%s", b.SourceID, b.ClientID, b.Reason)
	})
	return result, nil
}

func sameMaterial(source string, supplied []byte) bool {
	return source == "" || bytes.Equal([]byte(source), supplied)
}
