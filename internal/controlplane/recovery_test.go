package controlplane

import (
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestMasterRestoreFreezesNewAuthorityPostgres(t *testing.T) {
	for _, field := range []string{"authority", "desired", "applied", "usage"} {
		t.Run(field, func(t *testing.T) {
			store, db, nodes, client, term, budgets := setupLedger(t)
			p := nodes[0]
			hello := journalHello(t, store, p)
			switch field {
			case "authority":
				hello.AuthorityGeneration = 1
			case "desired":
				hello.DesiredRevision = 1
			case "applied":
				hello.AppliedRevision = 1
			case "usage":
				hello.DurableUsageSequence = 1
			}
			if _, err := store.BeginSession(t.Context(), p, hello); !errors.Is(err, ErrRecovery) {
				t.Fatalf("restored master accepted ahead node: %v", err)
			}
			var fence model.FleetFence
			if err := db.First(&fence, "id = 1").Error; err != nil {
				t.Fatal(err)
			}
			if !fence.RecoveryRequired || fence.Reason != "NODE_HIGH_WATER_AHEAD" || fence.WitnessNodeID == nil || *fence.WitnessNodeID != p.NodeID {
				t.Fatalf("recovery fence not durable: %+v", fence)
			}
			other := nodes[1]
			_, err := store.Reserve(t.Context(), ReserveRequest{
				RuntimeID:   "xray",
				OperationID: uuid.NewString(), GrantID: uuid.NewString(), NodeID: other.NodeID,
				InstallationID: other.InstallationID, ClientID: client, TermID: term, BudgetIDs: budgets, Bytes: 1,
			})
			if !errors.Is(err, ErrRecovery) {
				t.Fatalf("other node received fresh credit: %v", err)
			}
			if _, err := store.Publish(t.Context(), other.NodeID, []byte("configuration")); !errors.Is(err, ErrRecovery) {
				t.Fatalf("recovery published configuration: %v", err)
			}
			if _, err := store.Delivery(t.Context(), other, 1); !errors.Is(err, ErrRecovery) {
				t.Fatalf("existing session bypassed recovery: %v", err)
			}
		})
	}
}
