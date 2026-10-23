package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"strconv"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/cores/nodeconfig"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func prepareFleetNetworkAssets(tx *gorm.DB, state fleetNetworkState, input *nodeconfig.Input) ([]FleetNetworkAssetPin, []FleetNetworkDiagnostic, error) {
	slots := map[string]bool{}
	for _, inbound := range input.Inbounds {
		for _, slot := range inbound.Assets {
			slots[slot] = true
		}
	}
	for _, base := range input.Bases {
		for _, slot := range base.Assets {
			slots[slot] = true
		}
	}
	if input.SSHHostKeys.AssetID != "" {
		slots[input.SSHHostKeys.AssetID] = true
	}
	ordered := make([]string, 0, len(slots))
	for slot := range slots {
		ordered = append(ordered, slot)
	}
	slices.Sort(ordered)
	var pins []FleetNetworkAssetPin
	var issues []FleetNetworkDiagnostic
	var keys [][]any
	bindings := map[string]FleetAssetReference{}
	for _, slot := range ordered {
		ref, exists := state.Overrides.AssetBindings[slot]
		if !exists || state.Resolved.AssetSlots[slot] == "" {
			issues = append(issues, FleetNetworkDiagnostic{Code: "ASSET_BINDING_REQUIRED", AssetSlot: slot})
			continue
		}
		id := ref.AssetID + "-" + strconv.FormatUint(ref.Revision, 10)
		if _, seen := bindings[id]; !seen {
			keys = append(keys, []any{ref.AssetID, int64(ref.Revision)})
		}
		bindings[id] = ref
	}
	metadata := map[string]model.FleetNetworkAssetRevision{}
	var total int64
	for offset := 0; offset < len(keys); offset += 512 {
		var rows []struct {
			model.FleetNetworkAssetRevision
			Size int64
		}
		chunk := keys[offset:min(offset+512, len(keys))]
		if err := tx.Model(&model.FleetNetworkAssetRevision{}).Select("asset_id,revision,node_id,installation_id,kind,digest,octet_length(data) AS size").Where("(asset_id,revision) IN ?", chunk).Scan(&rows).Error; err != nil {
			return nil, nil, err
		}
		if len(rows) != len(chunk) {
			return nil, nil, ErrFleetNetworkDependency
		}
		for _, row := range rows {
			if row.NodeID != state.Node.ID || row.InstallationID != state.Node.InstallationID || row.Size < 1 || row.Size > 8<<20 || total > 32<<20-row.Size {
				return nil, nil, ErrFleetNetworkDependency
			}
			total += row.Size
			metadata[row.AssetID+"-"+strconv.FormatInt(row.Revision, 10)] = row.FleetNetworkAssetRevision
		}
	}
	for offset := 0; offset < len(keys); offset += 128 {
		var rows []model.FleetNetworkAssetRevision
		if err := tx.Where("(asset_id,revision) IN ?", keys[offset:min(offset+128, len(keys))]).Find(&rows).Error; err != nil {
			return nil, nil, err
		}
		for _, row := range rows {
			id := row.AssetID + "-" + strconv.FormatInt(row.Revision, 10)
			prior := metadata[id]
			digest := sha256.Sum256(row.Data)
			if !bytes.Equal(digest[:], prior.Digest) || !bytes.Equal(row.Digest, prior.Digest) || row.NodeID != state.Node.ID || row.InstallationID != state.Node.InstallationID {
				return nil, nil, ErrFleetNetworkDependency
			}
			input.Assets = append(input.Assets, nodeconfig.Asset{ID: id, NodeID: row.NodeID, Kind: row.Kind, Data: row.Data})
		}
	}
	resolve := func(slot string) string {
		ref, exists := state.Overrides.AssetBindings[slot]
		if !exists {
			return slot
		}
		return ref.AssetID + "-" + strconv.FormatUint(ref.Revision, 10)
	}
	for _, slot := range ordered {
		ref, exists := state.Overrides.AssetBindings[slot]
		if !exists {
			continue
		}
		row := metadata[resolve(slot)]
		if row.Kind != state.Resolved.AssetSlots[slot] {
			return nil, nil, ErrFleetNetworkDependency
		}
		pins = append(pins, FleetNetworkAssetPin{Slot: slot, AssetID: ref.AssetID, Revision: ref.Revision, Kind: row.Kind, SHA256: hex.EncodeToString(row.Digest)})
	}
	for index := range input.Inbounds {
		refs := map[string]string{}
		for field, slot := range input.Inbounds[index].Assets {
			refs[field] = resolve(slot)
		}
		input.Inbounds[index].Assets = refs
	}
	for index := range input.Bases {
		refs := map[string]string{}
		for field, slot := range input.Bases[index].Assets {
			refs[field] = resolve(slot)
		}
		input.Bases[index].Assets = refs
	}
	if input.SSHHostKeys.AssetID != "" {
		input.SSHHostKeys.AssetID = resolve(input.SSHHostKeys.AssetID)
	}
	return pins, issues, nil
}
