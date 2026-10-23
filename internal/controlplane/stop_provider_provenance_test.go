package controlplane

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/configuration"
)

func stopProviderFixture(t *testing.T) (stopFixture, model.FleetNetworkCompilation, model.FleetPublishedProviderBinding) {
	t.Helper()
	f := newStopFixture(t, true)
	pin := stopProviderPin{RuntimeID: "xray", ProviderID: uuid.NewString(), Revision: 7, ConfigurationRevision: 3, SHA256: hex.EncodeToString(sourceDigest("frozen provider cache")), FollowLatest: true}
	manifest, err := json.Marshal(map[string]any{"nodeId": f.node.NodeID, "installationId": f.node.InstallationID, "providers": []stopProviderPin{pin}})
	if err != nil {
		t.Fatal(err)
	}
	compiled := model.FleetNetworkCompilation{
		ID: uuid.NewString(), NodeID: f.node.NodeID, InstallationID: f.node.InstallationID,
		NodeRevisionID: uuid.NewString(), DefaultsRevisionID: uuid.NewString(), CompilerVersion: "test", Manifest: string(manifest),
		RuntimeDefinitions: `[]`, Preview: `{}`, Digest: sourceDigest("frozen network"), Ready: true, CreatedUnixMilli: 1, ActorID: 1,
	}
	if err := f.db.Create(&compiled).Error; err != nil {
		t.Fatal(err)
	}
	publication := model.FleetNetworkPublication{OperationID: uuid.NewString(), CompilationID: compiled.ID, NodeID: f.node.NodeID, DeploymentRevision: 1, CreatedUnixMilli: 1}
	if err := f.db.Create(&publication).Error; err != nil {
		t.Fatal(err)
	}
	// No provider/cache source rows exist; carry-forward must use frozen evidence only.
	binding := model.FleetPublishedProviderBinding{
		NodeID: f.node.NodeID, InstallationID: f.node.InstallationID, DeploymentRevision: 1,
		RuntimeID: pin.RuntimeID, ProviderID: pin.ProviderID, CompilationID: compiled.ID, ProviderRevision: int64(pin.Revision), ConfigurationRevision: int64(pin.ConfigurationRevision),
		ProviderDigest: sourceDigest("frozen provider cache"), FollowLatest: true, ScopeKnown: true, ReadyUnixMilli: 123, Diagnostic: "PREVIOUS_WAKE_HELD",
	}
	if err := f.db.Create(&binding).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.Model(&binding).Update("pending", false).Error; err != nil {
		t.Fatal(err)
	}
	binding.Pending = false
	return f, compiled, binding
}

func TestStopConfigurationCarriesFrozenProviderBindingsPostgres(t *testing.T) {
	f, _, original := stopProviderFixture(t)
	generation, deployment := deliverStop(t, f)
	var carried, old model.FleetPublishedProviderBinding
	if err := f.db.First(&carried, "node_id = ? AND deployment_revision = 2", f.node.NodeID).Error; err != nil {
		t.Fatal(err)
	}
	want := original
	want.DeploymentRevision, want.Pending, want.ReadyUnixMilli = 2, true, 0
	want.Diagnostic = ""
	if !reflect.DeepEqual(carried, want) {
		t.Fatalf("stop changed frozen provider scope: got=%+v want=%+v", carried, want)
	}
	if err := f.db.First(&old, "node_id = ? AND deployment_revision = 1", f.node.NodeID).Error; err != nil || !reflect.DeepEqual(old, original) {
		t.Fatalf("stop changed original provider binding: %+v %v", old, err)
	}
	var oldRuntime, newRuntime model.FleetPublishedRuntime
	if err := f.db.First(&oldRuntime, "node_id = ? AND revision = 1 AND runtime_id = 'xray'", f.node.NodeID).Error; err != nil {
		t.Fatal(err)
	}
	if err := f.db.First(&newRuntime, "node_id = ? AND revision = 2 AND runtime_id = 'xray'", f.node.NodeID).Error; err != nil {
		t.Fatal(err)
	}
	projection, err := configuration.Project(deployment.Payload, "xray", "xray")
	if err != nil {
		t.Fatal(err)
	}
	projected := sha256.Sum256(projection)
	if bytes.Equal(oldRuntime.PayloadDigest, newRuntime.PayloadDigest) || !bytes.Equal(newRuntime.PayloadDigest, projected[:]) || !bytes.Equal(newRuntime.DeploymentDigest, deployment.PayloadSha256) {
		t.Fatal("stop retained old authority-envelope projection hashes")
	}
	if err := f.store.Observe(t.Context(), f.node, generation, appliedStop(generation, deployment)); err != nil {
		t.Fatal(err)
	}
	var pending int64
	if err := f.db.Model(&model.FleetPublishedProviderBinding{}).Where("node_id = ? AND deployment_revision = 2 AND pending AND ready_unix_milli = 0", f.node.NodeID).Count(&pending).Error; err != nil || pending != 1 {
		t.Fatalf("APPLIED lost the late provider wake: %d %v", pending, err)
	}
	replay, err := f.store.StopConfiguration(t.Context(), f.node, f.request, StopConfigurationValidators{})
	if err != nil || !replay.Replayed || replay.Revision != 2 {
		t.Fatalf("exact stop retry changed provider publication: %+v %v", replay, err)
	}
}

func TestStopConfigurationRefusesUnprovenProviderBindingsPostgres(t *testing.T) {
	for _, test := range []struct {
		name    string
		changes map[string]any
	}{
		{"missing", nil},
		{"unknown", map[string]any{"scope_known": false}},
		{"foreign installation", map[string]any{"installation_id": uuid.NewString()}},
		{"foreign compilation", map[string]any{"compilation_id": uuid.NewString()}},
		{"foreign runtime", map[string]any{"runtime_id": "native"}},
		{"changed cache", map[string]any{"provider_digest": sourceDigest("different cache")}},
		{"changed revision", map[string]any{"provider_revision": 8}},
		{"changed configuration", map[string]any{"configuration_revision": 4}},
		{"changed following", map[string]any{"follow_latest": false}},
	} {
		t.Run(test.name, func(t *testing.T) {
			f, _, _ := stopProviderFixture(t)
			query := f.db.Model(&model.FleetPublishedProviderBinding{}).Where("node_id = ? AND deployment_revision = 1", f.node.NodeID)
			var err error
			if test.changes == nil {
				err = query.Delete(&model.FleetPublishedProviderBinding{}).Error
			} else {
				err = query.Updates(test.changes).Error
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.StopConfiguration(t.Context(), f.node, f.request, stopValidators()); !errors.Is(err, ErrStopComposition) {
				t.Fatalf("unproven provider scope accepted: %v", err)
			}
			var node model.FleetNode
			if err := f.db.First(&node, "id = ?", f.node.NodeID).Error; err != nil || node.DesiredRevision != 1 {
				t.Fatalf("failed carry-forward advanced publication: revision=%d err=%v", node.DesiredRevision, err)
			}
		})
	}
}

func TestStopProviderCarryRequiresFrozenGraphAndFreshProjectionPostgres(t *testing.T) {
	for _, kind := range []string{"stale projection", "changed graph", "missing runtime"} {
		t.Run(kind, func(t *testing.T) {
			f, compiled, _ := stopProviderFixture(t)
			_, _ = deliverStop(t, f)
			err := f.db.Transaction(func(tx *gorm.DB) error {
				if err := tx.Where("node_id = ? AND deployment_revision = 2", f.node.NodeID).Delete(&model.FleetPublishedProviderBinding{}).Error; err != nil {
					return err
				}
				if kind == "stale projection" {
					var before model.FleetPublishedRuntime
					if err := tx.First(&before, "node_id = ? AND revision = 1 AND runtime_id = 'xray'", f.node.NodeID).Error; err != nil {
						return err
					}
					if err := tx.Model(&model.FleetPublishedRuntime{}).Where("node_id = ? AND revision = 2 AND runtime_id = 'xray'", f.node.NodeID).Update("payload_digest", before.PayloadDigest).Error; err != nil {
						return err
					}
				} else {
					var deployment model.FleetDeployment
					if err := tx.First(&deployment, "node_id = ? AND revision = 2", f.node.NodeID).Error; err != nil {
						return err
					}
					bundle, err := configuration.Decode(deployment.Payload)
					if err != nil {
						return err
					}
					for index := range bundle.Runtimes {
						if bundle.Runtimes[index].ID == "xray" {
							if kind == "missing runtime" {
								bundle.Runtimes = append(bundle.Runtimes[:index], bundle.Runtimes[index+1:]...)
							} else {
								bundle.Runtimes[index].Configuration = json.RawMessage(`{"different":"provider graph"}`)
							}
							break
						}
					}
					deployment.Payload, err = json.Marshal(bundle)
					if err != nil {
						return err
					}
					hash := sha256.Sum256(deployment.Payload)
					deployment.Digest = hash[:]
					if err := tx.Save(&deployment).Error; err != nil {
						return err
					}
					if err := tx.Where("node_id = ? AND revision = 2", f.node.NodeID).Delete(&model.FleetPublishedRuntime{}).Error; err != nil {
						return err
					}
					if err := tx.Where("node_id = ? AND revision = 2", f.node.NodeID).Delete(&model.FleetPublishedClientAdmission{}).Error; err != nil {
						return err
					}
					if err := tx.Where("node_id = ? AND revision = 2", f.node.NodeID).Delete(&model.FleetPublishedAdmissionBinding{}).Error; err != nil {
						return err
					}
					if err := indexDeploymentAdmissions(tx, model.FleetNode{ID: f.node.NodeID, InstallationID: f.node.InstallationID}, deployment); err != nil {
						return err
					}
				}
				return carryStopProviderBindings(tx, f.node, compiled, 1, 2)
			})
			if !errors.Is(err, ErrStopComposition) {
				t.Fatalf("provider scope %s was carried: %v", kind, err)
			}
		})
	}
}
