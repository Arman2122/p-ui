package controlplane

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/configuration"
)

func TestAppliedManifestMustMatchDesiredFilesPostgres(t *testing.T) {
	store, db, nodes, _, _, _ := setupLedger(t)
	node := nodes[0]
	generation, err := store.BeginSession(t.Context(), node, journalHello(t, store, node))
	if err != nil {
		t.Fatal(err)
	}
	for _, files := range []bool{false, true} {
		bundle := configuration.Bundle{Version: 1, Runtimes: []configuration.Runtime{{ID: "ssh", Core: "ssh", Configuration: json.RawMessage(`{}`)}}}
		if files {
			data := []byte("synthetic certificate")
			digest := sha256.Sum256(data)
			bundle.Runtimes[0].FileAssets = []configuration.FileAsset{{ID: "cert", SHA256: hex.EncodeToString(digest[:]), Data: data}}
		}
		if _, err := store.PublishConfiguration(t.Context(), node, bundle); err != nil {
			t.Fatal(err)
		}
		deployment, err := store.Delivery(t.Context(), node, generation)
		if err != nil {
			t.Fatal(err)
		}
		result := &nodev1.ApplyResult{AuthorityGeneration: generation, Revision: deployment.Revision, PayloadSha256: deployment.PayloadSha256, Stage: nodev1.ApplyStage_APPLY_STAGE_APPLIED}
		for _, bad := range [][]byte{nil, bytes.Repeat([]byte{1}, 32)} {
			result.AssetManifestSha256 = bad
			if err := store.Observe(t.Context(), node, generation, result); !errors.Is(err, ErrRevision) {
				t.Fatalf("incorrect manifest accepted: %v", err)
			}
			var record model.FleetNode
			if err := db.First(&record, "id = ?", node.NodeID).Error; err != nil {
				t.Fatal(err)
			}
			if uint64(record.AppliedRevision) >= deployment.Revision {
				t.Fatal("failed receipt advanced applied revision")
			}
		}
		result.AssetManifestSha256 = configuration.PayloadAssetManifest(deployment.Payload)
		if err := store.Observe(t.Context(), node, generation, result); err != nil {
			t.Fatal(err)
		}
		if err := store.Observe(t.Context(), node, generation, result); err != nil {
			t.Fatalf("exact receipt replay: %v", err)
		}
	}
}
