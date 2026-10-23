package service

import (
	"testing"
	"time"

	"github.com/google/uuid"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestFleetStatusSeparatesConnectivityFromCurrentRuntimeObservations(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()
	now := time.Now().UnixMilli()
	node := model.FleetNode{
		ID: uuid.NewString(), InstallationID: uuid.NewString(), Name: "observed-node", Enabled: true,
		AuthorityGeneration: 2, DesiredRevision: 2, AppliedRevision: 1, LastSeenUnixMilli: now,
	}
	if err := db.Create(&node).Error; err != nil {
		t.Fatal(err)
	}
	observation := model.FleetNodeObservation{
		NodeID: node.ID, RuntimeGeneration: 1, ReceivedUnixMilli: now,
		ObservedUnixMilli: now + 86400000, ConditionsJSON: `[{"resource_id":"xray","condition":"CONFIGURATION_APPLIED","ready":true}]`,
		ResultGeneration: 2, ResultRevision: 2, ResultStage: int32(nodev1.ApplyStage_APPLY_STAGE_REJECTED), ResultCode: "APPLY_FAILED",
	}
	if err := db.Create(&observation).Error; err != nil {
		t.Fatal(err)
	}
	check := func(want string) {
		t.Helper()
		page, err := FleetNodes(t.Context(), "", 200)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 1 || !page.Items[0].Online || page.Items[0].DeploymentState != "rejected" || page.Items[0].RuntimeState != want {
			t.Fatalf("master node status = %+v, want connected/rejected/%s", page, want)
		}
	}
	check("stale")
	if err := db.Model(&observation).Update("runtime_generation", 2).Error; err != nil {
		t.Fatal(err)
	}
	check("ready")
	if err := db.Model(&observation).Update("conditions_json", `[{"resource_id":"xray","condition":"RESTORE_FAILED","ready":false}]`).Error; err != nil {
		t.Fatal(err)
	}
	check("degraded")
	if err := db.Model(&observation).Update("received_unix_milli", now-60000).Error; err != nil {
		t.Fatal(err)
	}
	check("stale")
}
