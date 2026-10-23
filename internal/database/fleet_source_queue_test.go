package database

import (
	"testing"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestFleetSourceQueueIndexUpgradePreservesPendingWorkPostgres(t *testing.T) {
	initTestDB(t)
	if err := db.Exec("DROP INDEX idx_fleet_source_work_current_due").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE INDEX idx_fleet_source_work_due ON fleet_source_works (node_id, requires_stop DESC, available_unix_milli) WHERE pending").Error; err != nil {
		t.Fatal(err)
	}
	work := model.FleetSourceWork{
		PlacementID: uuid.NewString(), ClientID: uuid.NewString(), NodeID: uuid.NewString(), InstallationID: uuid.NewString(),
		OperationID: uuid.NewString(), RuntimeID: "xray", Revision: 7, SourceRevision: 11, Enabled: true,
		RequiresStop: true, AppliedStopRevision: 3, Pending: true, QueuedUnixMilli: 100, AvailableUnixMilli: 200,
	}
	if err := db.Create(&work).Error; err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := initModels(); err != nil {
			t.Fatal(err)
		}
		var restored model.FleetSourceWork
		if err := db.First(&restored, "placement_id = ?", work.PlacementID).Error; err != nil || restored != work {
			t.Fatalf("queue index migration changed pending authority intent=%+v err=%v", restored, err)
		}
		if db.Migrator().HasIndex(&model.FleetSourceWork{}, "idx_fleet_source_work_due") || !db.Migrator().HasIndex(&model.FleetSourceWork{}, "idx_fleet_source_work_current_due") {
			t.Fatal("queue migration retained old index or missed installation-scoped index")
		}
	}
}
