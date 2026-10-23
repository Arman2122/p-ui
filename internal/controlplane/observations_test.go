package controlplane

import (
	"errors"
	"strings"
	"testing"
	"time"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestObservationsAreFencedAndCannotAcknowledgeDeployment(t *testing.T) {
	store, db, nodes, _, _, _ := setupLedger(t)
	node := nodes[0]
	hello := journalHello(t, store, node)
	generation, err := store.BeginSession(t.Context(), node, hello)
	if err != nil {
		t.Fatal(err)
	}
	revision, err := store.Publish(t.Context(), node.NodeID, []byte("authorized configuration"))
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.Delivery(t.Context(), node, generation)
	if err != nil {
		t.Fatal(err)
	}
	rejected := &nodev1.ApplyResult{
		AuthorityGeneration: generation, Revision: revision, PayloadSha256: deployment.PayloadSha256,
		Stage: nodev1.ApplyStage_APPLY_STAGE_REJECTED, ErrorCode: "secret-password", ErrorMessage: "secret-password",
	}
	if err := store.Observe(t.Context(), node, generation, rejected); err != nil {
		t.Fatal(err)
	}
	heartbeat := &nodev1.Heartbeat{
		ObservedUnixMilli: time.Now().Add(24 * time.Hour).UnixMilli(), AppliedRevision: revision,
		Conditions: []*nodev1.RuntimeCondition{{ResourceId: "xray", Condition: "RESTORE_FAILED", Detail: "secret-password"}},
	}
	if err := store.Heartbeat(t.Context(), node, generation, heartbeat); err != nil {
		t.Fatal(err)
	}
	var record model.FleetNode
	if err := db.First(&record, "id = ?", node.NodeID).Error; err != nil {
		t.Fatal(err)
	}
	if record.AppliedRevision != 0 {
		t.Fatal("heartbeat acknowledged a deployment without an applied result")
	}
	rows, err := store.Observations(t.Context(), []string{node.NodeID})
	if err != nil {
		t.Fatal(err)
	}
	observed := rows[node.NodeID]
	if observed.ResultCode != "APPLY_FAILED" || observed.ResultStage != int32(nodev1.ApplyStage_APPLY_STAGE_REJECTED) ||
		observed.ReportedAppliedRevision != int64(revision) || strings.Contains(observed.ConditionsJSON, "secret-password") ||
		observed.ReceivedUnixMilli >= heartbeat.ObservedUnixMilli || observed.RuntimeGeneration != int64(generation) {
		t.Fatalf("incorrect observation: %+v", observed)
	}
	rejected.Stage = nodev1.ApplyStage_APPLY_STAGE_RECEIVED
	if err := store.Observe(t.Context(), node, generation, rejected); err != nil {
		t.Fatal(err)
	}
	rows, err = store.Observations(t.Context(), []string{node.NodeID})
	if err != nil || rows[node.NodeID].ResultStage != int32(nodev1.ApplyStage_APPLY_STAGE_REJECTED) {
		t.Fatalf("retry erased the last failure: %+v %v", rows, err)
	}
	hello.AuthorityGeneration = generation
	if _, err := store.BeginSession(t.Context(), node, hello); err != nil {
		t.Fatal(err)
	}
	if err := store.Heartbeat(t.Context(), node, generation, heartbeat); !errors.Is(err, ErrScope) {
		t.Fatalf("superseded session observation = %v", err)
	}
	heartbeat.Conditions = append(heartbeat.Conditions, heartbeat.Conditions[0])
	if err := store.Heartbeat(t.Context(), node, generation+1, heartbeat); !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate condition = %v", err)
	}
	heartbeat.Conditions = heartbeat.Conditions[:1]
	heartbeat.AppliedRevision++
	if err := store.Heartbeat(t.Context(), node, generation+1, heartbeat); !errors.Is(err, ErrRevision) {
		t.Fatalf("future revision observation = %v", err)
	}
}
