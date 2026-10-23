package database

import (
	"bytes"
	"crypto/sha256"
	"testing"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestFleetPublishedScopeCompletenessMigrationDefaultsUnknownPostgres(t *testing.T) {
	initTestDB(t)
	node := model.FleetNode{ID: uuid.NewString(), InstallationID: uuid.NewString(), Name: "old-projection", Enabled: true}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"version":1,"runtimes":[]}`)
	digest := sha256.Sum256(payload)
	if err := db.Create(&model.FleetDeployment{NodeID: node.ID, Revision: 1, Digest: digest[:], Payload: payload}).Error; err != nil {
		t.Fatal(err)
	}
	row := model.FleetPublishedRuntime{NodeID: node.ID, Revision: 1, RuntimeID: "xray", CoreID: "xray", DeploymentDigest: digest[:], PayloadDigest: digest[:], GrantScopesComplete: true}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Migrator().DropColumn(&model.FleetPublishedRuntime{}, "GrantScopesComplete"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := migrateFleetPublishedRuntimeCompleteness(); err != nil {
			t.Fatal(err)
		}
		var restored model.FleetPublishedRuntime
		if err := db.First(&restored, "node_id = ? AND revision = 1 AND runtime_id = 'xray'", node.ID).Error; err != nil {
			t.Fatal(err)
		}
		if restored.GrantScopesComplete || restored.CoreID != row.CoreID || !bytes.Equal(restored.DeploymentDigest, row.DeploymentDigest) || !bytes.Equal(restored.PayloadDigest, row.PayloadDigest) {
			t.Fatalf("old publication was trusted without exact reindexing: %+v", restored)
		}
	}
}
