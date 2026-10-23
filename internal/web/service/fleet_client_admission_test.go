package service

import (
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/configuration"
)

func TestFleetAdmissionPublicationRequiresCurrentPlacementScope(t *testing.T) {
	f := fleetFixture(t, true)
	materialized := f.materialize(t, 0)
	f.approve(t, materialized)
	bundle := fleetAdmissionBundle(materialized)
	for _, tc := range []struct {
		name   string
		change func(*configuration.Bundle)
	}{
		{"credential generation", func(value *configuration.Bundle) { value.Runtimes[0].Admissions[0].CredentialGeneration++ }},
		{"placement revision", func(value *configuration.Bundle) { value.Runtimes[0].Admissions[0].PlacementRevision++ }},
		{"inbound instance", func(value *configuration.Bundle) {
			value.Runtimes[0].Admissions[0].InboundInstanceID = uuid.NewString()
		}},
		{"client identity", func(value *configuration.Bundle) { value.Runtimes[0].Admissions[0].ClientID = uuid.NewString() }},
		{"runtime core", func(value *configuration.Bundle) { value.Runtimes[0].Core = "ssh" }},
	} {
		candidate := bundle
		candidate.Runtimes = slices.Clone(bundle.Runtimes)
		candidate.Runtimes[0].Admissions = slices.Clone(bundle.Runtimes[0].Admissions)
		tc.change(&candidate)
		if _, err := f.store.PublishConfiguration(t.Context(), f.nodes[0], candidate); !errors.Is(err, controlplane.ErrScope) {
			t.Fatalf("foreign admission %s published=%v", tc.name, err)
		}
	}
	if _, err := f.store.PublishConfiguration(t.Context(), f.nodes[1], bundle); !errors.Is(err, controlplane.ErrScope) {
		t.Fatalf("foreign admission node published=%v", err)
	}
	rollback := errors.New("restore placement authority")
	for _, field := range []string{"owned", "enabled"} {
		if err := f.db.Transaction(func(tx *gorm.DB) error {
			var row any = &model.FleetClient{ID: materialized.Client.ID}
			if field == "enabled" {
				row = &model.FleetPlacement{ID: materialized.Placement.ID}
			}
			if err := tx.Model(row).Update(field, false).Error; err != nil {
				return err
			}
			store, err := controlplane.NewStore(tx)
			if err != nil {
				return err
			}
			if _, err := store.PublishConfiguration(t.Context(), f.nodes[0], bundle); !errors.Is(err, controlplane.ErrScope) {
				t.Errorf("inactive admission %s published=%v", field, err)
			}
			return rollback
		}); !errors.Is(err, rollback) {
			t.Fatal(err)
		}
	}
	if revision, err := f.store.PublishConfiguration(t.Context(), f.nodes[0], bundle); err != nil || revision != 1 {
		t.Fatalf("scoped dormant descriptor=%d err=%v", revision, err)
	}
	for _, table := range []any{&model.FleetTerm{}, &model.FleetGrant{}} {
		var count int64
		if err := f.db.Model(table).Count(&count).Error; err != nil || count != 0 {
			t.Fatalf("descriptor publication created authorization %T=%d err=%v", table, count, err)
		}
	}
}
