package controlplane

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestStopConfigurationCarriesOnlyExactBaseProvenancePostgres(t *testing.T) {
	for _, scope := range []string{"exact", "absent", "foreign node", "foreign installation", "ambiguous"} {
		t.Run(scope, func(t *testing.T) {
			f := newStopFixture(t, true)
			compilation := model.FleetNetworkCompilation{
				ID: uuid.NewString(), NodeID: f.node.NodeID, InstallationID: f.node.InstallationID,
				NodeRevisionID: uuid.NewString(), DefaultsRevisionID: uuid.NewString(), CompilerVersion: "test", Manifest: `{"nativeBindings":[{"id":"retained-native-binding"}]}`,
				RuntimeDefinitions: `[]`, Preview: `{}`, Digest: sourceDigest("frozen compilation"), Ready: true, CreatedUnixMilli: 1, ActorID: 1,
			}
			if scope != "absent" {
				switch scope {
				case "foreign node":
					compilation.NodeID = uuid.NewString()
				case "foreign installation":
					compilation.InstallationID = uuid.NewString()
				}
				if err := f.db.Create(&compilation).Error; err != nil {
					t.Fatal(err)
				}
				publication := model.FleetNetworkPublication{OperationID: uuid.NewString(), CompilationID: compilation.ID, NodeID: f.node.NodeID, DeploymentRevision: 1, CreatedUnixMilli: 1}
				if err := f.db.Create(&publication).Error; err != nil {
					t.Fatal(err)
				}
				if scope == "ambiguous" {
					publication.OperationID, publication.CompilationID = uuid.NewString(), uuid.NewString()
					if err := f.db.Create(&publication).Error; err != nil {
						t.Fatal(err)
					}
				}
			}
			result, err := f.store.StopConfiguration(t.Context(), f.node, f.request, stopValidators())
			if scope != "exact" && scope != "absent" {
				if !errors.Is(err, ErrStopComposition) {
					t.Fatalf("foreign or ambiguous provenance accepted: %+v %v", result, err)
				}
				var node model.FleetNode
				if err := f.db.First(&node, "id = ?", f.node.NodeID).Error; err != nil || node.DesiredRevision != 1 {
					t.Fatalf("failed provenance changed node revision: %+v %v", node, err)
				}
				return
			}
			if err != nil || result.Revision != 2 {
				t.Fatalf("stop provenance publication=%+v %v", result, err)
			}
			var rows []model.FleetNetworkPublication
			if err := f.db.Where("node_id = ? AND deployment_revision = 2", f.node.NodeID).Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			if scope == "absent" {
				if len(rows) != 0 {
					t.Fatal("stop fabricated source provenance where the base had none")
				}
				return
			}
			if len(rows) != 1 || rows[0].OperationID != f.request.OperationID || rows[0].CompilationID != compilation.ID {
				t.Fatalf("stop lost exact source pins: %+v", rows)
			}
			var saved model.FleetNetworkCompilation
			if err := f.db.First(&saved, "id = ?", compilation.ID).Error; err != nil || saved.RuntimeDefinitions != compilation.RuntimeDefinitions || !bytes.Equal(saved.Digest, compilation.Digest) {
				t.Fatalf("stop changed compilation provenance: %+v %v", saved, err)
			}
			var wantManifest, gotManifest any
			if err := json.Unmarshal([]byte(compilation.Manifest), &wantManifest); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(saved.Manifest), &gotManifest); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(wantManifest, gotManifest) {
				t.Fatal("stop erased binding provenance without an applied graph removal")
			}
		})
	}
}
