package controlplane

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/configuration"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

type stopProviderPin struct {
	RuntimeID             string `json:"runtimeId"`
	ProviderID            string `json:"providerId"`
	Revision              uint64 `json:"revision"`
	ConfigurationRevision uint64 `json:"configurationRevision"`
	SHA256                string `json:"sha256"`
	FollowLatest          bool   `json:"followLatest"`
}

func carryStopProviderBindings(tx *gorm.DB, principal identity.Principal, compilation model.FleetNetworkCompilation, base, revision int64) error {
	var manifest struct {
		NodeID         string            `json:"nodeId"`
		InstallationID string            `json:"installationId"`
		Providers      []stopProviderPin `json:"providers"`
	}
	if json.Unmarshal([]byte(compilation.Manifest), &manifest) != nil || len(manifest.Providers) > 1024 {
		return ErrStopComposition
	}
	var rows []model.FleetPublishedProviderBinding
	if err := tx.Where("node_id = ? AND deployment_revision = ?", principal.NodeID, base).Limit(1025).Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) != len(manifest.Providers) {
		return ErrStopComposition
	}
	if len(rows) == 0 {
		return nil
	}
	if manifest.NodeID != principal.NodeID || manifest.InstallationID != principal.InstallationID {
		return ErrStopComposition
	}
	pins := make(map[string]stopProviderPin, len(manifest.Providers))
	runtimes := map[string]bool{}
	for _, pin := range manifest.Providers {
		key := pin.RuntimeID + "/" + pin.ProviderID
		if _, duplicate := pins[key]; duplicate || !validID(pin.ProviderID) || pin.Revision == 0 || pin.Revision > math.MaxInt64 || pin.ConfigurationRevision == 0 || pin.ConfigurationRevision > math.MaxInt64 {
			return ErrStopComposition
		}
		pins[key], runtimes[pin.RuntimeID] = pin, true
	}
	for index := range rows {
		row := &rows[index]
		key := row.RuntimeID + "/" + row.ProviderID
		pin, exists := pins[key]
		if !exists || !row.ScopeKnown || row.InstallationID != principal.InstallationID || row.CompilationID != compilation.ID ||
			row.ProviderRevision != int64(pin.Revision) || row.ConfigurationRevision != int64(pin.ConfigurationRevision) ||
			row.FollowLatest != pin.FollowLatest || len(row.ProviderDigest) != sha256.Size || hex.EncodeToString(row.ProviderDigest) != pin.SHA256 {
			return ErrStopComposition
		}
		delete(pins, key)
		row.DeploymentRevision, row.Pending, row.ReadyUnixMilli = revision, true, 0
		row.Diagnostic = ""
	}
	if len(pins) != 0 {
		return ErrStopComposition
	}
	before, err := stopProviderGraphs(tx, principal.NodeID, base, runtimes)
	if err != nil {
		return err
	}
	after, err := stopProviderGraphs(tx, principal.NodeID, revision, runtimes)
	if err != nil {
		return err
	}
	for id, graph := range before {
		if !bytes.Equal(graph, after[id]) {
			return ErrStopComposition
		}
	}
	return tx.CreateInBatches(rows, 128).Error
}

// Authority changes alter projected payload hashes. Each revision must prove its
// own projection while its provider graph, files and admission scope stay frozen.
func stopProviderGraphs(tx *gorm.DB, nodeID string, revision int64, required map[string]bool) (map[string][]byte, error) {
	var deployment model.FleetDeployment
	if err := tx.First(&deployment, "node_id = ? AND revision = ?", nodeID, revision).Error; err != nil {
		return nil, err
	}
	hash := sha256.Sum256(deployment.Payload)
	bundle, err := configuration.Decode(deployment.Payload)
	if err != nil || !bytes.Equal(hash[:], deployment.Digest) {
		return nil, ErrStopComposition
	}
	var indexes []model.FleetPublishedRuntime
	if err := tx.Where("node_id = ? AND revision = ?", nodeID, revision).Find(&indexes).Error; err != nil {
		return nil, err
	}
	byID := make(map[string]model.FleetPublishedRuntime, len(indexes))
	for _, index := range indexes {
		byID[index.RuntimeID] = index
	}
	graphs := make(map[string][]byte, len(required))
	for _, runtime := range bundle.Runtimes {
		if !required[runtime.ID] {
			continue
		}
		index, exists := byID[runtime.ID]
		if !exists || runtime.Core != "xray" || index.CoreID != runtime.Core || !bytes.Equal(index.DeploymentDigest, hash[:]) {
			return nil, ErrStopComposition
		}
		projection, err := configuration.ProjectBundle(bundle, runtime.ID, runtime.Core)
		if err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(projection)
		if err != nil {
			return nil, err
		}
		projected := sha256.Sum256(encoded)
		if !bytes.Equal(index.PayloadDigest, projected[:]) {
			return nil, ErrStopComposition
		}
		runtime.Grants = nil
		graphs[runtime.ID], err = json.Marshal(runtime)
		if err != nil {
			return nil, err
		}
	}
	if len(graphs) != len(required) {
		return nil, ErrStopComposition
	}
	return graphs, nil
}
