package service

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

func TestFleetProjectionKeepsDirectionsReservationAndHistoryDistinct(t *testing.T) {
	for _, unlimited := range []bool{false, true} {
		name := "finite"
		if unlimited {
			name = "unlimited"
		}
		t.Run(name, func(t *testing.T) {
			f := fleetFixture(t, false)
			if unlimited {
				if err := f.db.Model(&f.client).Update("total_gb", 0).Error; err != nil {
					t.Fatal(err)
				}
			}
			if err := f.db.Model(&core.ClientTraffic{}).Where("email = ?", f.client.Email).Updates(map[string]any{"up": 50, "down": 70, "total": f.client.TotalGB}).Error; err != nil {
				t.Fatal(err)
			}
			placement := f.approve(t, f.materialize(t, 0))
			rows := map[string]*core.ClientTraffic{
				f.client.Email: {Email: f.client.Email, Up: 9000, Down: 8000},
				"legacy":       {Email: "legacy", Up: 100, Down: 200},
			}
			projected, err := withFleetTraffic(f.db, []*core.ClientTraffic{rows["legacy"], rows[f.client.Email]}, []string{f.client.Email, "legacy"})
			if err != nil || len(projected) != 2 || projected[0].Email != f.client.Email || projected[1].Email != "legacy" {
				t.Fatalf("batched projection lost or duplicated a source row: %+v %v", projected, err)
			}
			dormant := rows[f.client.Email]
			if dormant.Up != 50 || dormant.Down != 70 || dormant.Accounting.State != "awaiting_activation" || dormant.Accounting.ReservedBytes != nil || dormant.Accounting.UncertainBytes != nil {
				t.Fatalf("dormant source invented or inherited global usage=%+v %+v", dormant, dormant.Accounting)
			}
			active := f.activate(t, placement)
			credit, err := (FleetClientService{}).AllocateCredit(t.Context(), FleetCreditRequest{
				OperationID: uuid.NewString(), PlacementID: placement.ID, TermID: active.Term.ID,
				ExpectedPlacementRevision: 1, ExpectedCredentialGeneration: 1, Bytes: 300,
				Rates: controlplane.Rates{Upload: limits.Rate{Mode: limits.Unlimited}, Download: limits.Rate{Mode: limits.Unlimited}},
			})
			if err != nil {
				t.Fatal(err)
			}
			fleetUsageReport(t, f, 0, credit.Grant, 1, 30, 40)
			fleetUsageReport(t, f, 0, credit.Grant, 2, 30, 40)
			if err := f.db.Model(&f.client).Updates(map[string]any{"total_gb": 99999, "expiry_time": 1}).Error; err != nil {
				t.Fatal(err)
			}
			if err := projectFleetTraffic(f.db, []string{f.client.Email, "legacy"}, rows); err != nil {
				t.Fatal(err)
			}
			got, meta := rows[f.client.Email], rows[f.client.Email].Accounting
			if got.Up != 80 || got.Down != 110 || got.ExpiryTime != active.Term.ExpiresUnixMilli || got.Total != 1000 && !unlimited || unlimited && got.Total != 0 || meta.MeasuredUploaded != 30 || meta.MeasuredDownloaded != 40 || meta.OpeningUploaded != 50 || meta.OpeningDownloaded != 70 || meta.UncertainBytes != nil {
				t.Fatalf("projection mixed source intent with committed term=%+v %+v", got, meta)
			}
			if unlimited && meta.ReservedBytes != nil || !unlimited && (meta.ReservedBytes == nil || *meta.ReservedBytes != 230) {
				t.Fatalf("reservation semantics=%+v", meta)
			}
			if legacy := rows["legacy"]; legacy.Up != 100 || legacy.Down != 200 || legacy.Accounting != nil {
				t.Fatalf("unowned projection changed=%+v", legacy)
			}
			history := map[string][]ClientHistoryPoint{f.client.Email: {{T: 1, Up: 99999}}, "legacy": {{T: 1, Up: 200}}}
			if err := projectFleetHistories(f.db, []string{f.client.Email, "legacy"}, time.Now(), history); err != nil || len(history[f.client.Email]) != 1 || history[f.client.Email][0].Up != 30 || history[f.client.Email][0].Down != 40 || history["legacy"][0].Up != 200 {
				t.Fatalf("history included opening, stale legacy, or replay traffic=%+v err=%v", history, err)
			}
			if err := f.db.Delete(&model.FleetTermUsage{}, "term_id = ?", active.Term.ID).Error; err != nil {
				t.Fatal(err)
			}
			if err := projectFleetTraffic(f.db, []string{f.client.Email}, rows); !errors.Is(err, ErrFleetUsage) {
				t.Fatalf("missing aggregate silently fell back to legacy traffic=%v", err)
			}
		})
	}
}
