package controlplane

import (
	"testing"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestFleetListingHasNoMissingOrRepeatedNodes(t *testing.T) {
	db := testDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	nodes := make([]model.FleetNode, 401)
	for i := range nodes {
		nodes[i] = model.FleetNode{ID: uuid.NewString(), InstallationID: uuid.NewString(), Name: "fleet-node"}
	}
	if err := db.CreateInBatches(nodes, 200).Error; err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]bool)
	after := ""
	for page := 0; page < 3; page++ {
		rows, next, err := store.Nodes(t.Context(), after, 200)
		if err != nil {
			t.Fatal(err)
		}
		want := 200
		if page == 2 {
			want = 1
		}
		if len(rows) != want {
			t.Fatalf("page %d contains %d nodes, want %d", page, len(rows), want)
		}
		for _, node := range rows {
			if seen[node.ID] {
				t.Fatalf("node repeated: %s", node.ID)
			}
			seen[node.ID] = true
		}
		if page < 2 && next == "" || page == 2 && next != "" {
			t.Fatalf("page %d continuation = %q", page, next)
		}
		after = next
	}
	if len(seen) != len(nodes) {
		t.Fatalf("listed %d of %d nodes", len(seen), len(nodes))
	}
}
