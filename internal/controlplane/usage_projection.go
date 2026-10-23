package controlplane

import (
	"slices"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database/model"
)

type termUsageDelta struct {
	Uploaded, Downloaded, Observed int64
}

func recordTermUsage(tx *gorm.DB, deltas map[[2]string]*termUsageDelta) error {
	ids := make([]string, 0, len(deltas))
	owners := make(map[string]string, len(deltas))
	for identity := range deltas {
		if owner := owners[identity[1]]; owner != "" && owner != identity[0] {
			return ErrScope
		}
		owners[identity[1]] = identity[0]
		ids = append(ids, identity[1])
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	now := time.Now()
	var usage []model.FleetTermUsage
	var history []model.FleetTermUsageDay
	for offset := 0; offset < len(ids); offset += 1024 {
		var terms []model.FleetTerm
		if err := tx.Select("id, client_id").Where("id IN ?", ids[offset:min(offset+1024, len(ids))]).Order("id").Find(&terms).Error; err != nil {
			return err
		}
		for _, term := range terms {
			if owners[term.ID] != term.ClientID {
				return ErrScope
			}
			delta := deltas[[2]string{term.ClientID, term.ID}]
			if delta == nil {
				return ErrScope
			}
			usage = append(usage, model.FleetTermUsage{
				TermID: term.ID, Uploaded: delta.Uploaded, Downloaded: delta.Downloaded,
				ObservedUnixMilli: min(now.UnixMilli(), delta.Observed), ReceivedUnixMilli: now.UnixMilli(),
			})
			history = append(history, model.FleetTermUsageDay{TermID: term.ID, Day: now.Unix() / 86400 * 86400, Uploaded: delta.Uploaded, Downloaded: delta.Downloaded})
		}
	}
	if len(usage) == 0 {
		return nil
	}
	if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "term_id"}}, DoUpdates: clause.Assignments(map[string]any{
		"uploaded": gorm.Expr("fleet_term_usages.uploaded + EXCLUDED.uploaded"), "downloaded": gorm.Expr("fleet_term_usages.downloaded + EXCLUDED.downloaded"),
		"observed_unix_milli": gorm.Expr("GREATEST(fleet_term_usages.observed_unix_milli, EXCLUDED.observed_unix_milli)"),
		"received_unix_milli": gorm.Expr("GREATEST(fleet_term_usages.received_unix_milli, EXCLUDED.received_unix_milli)"),
	})}).CreateInBatches(usage, 500).Error; err != nil {
		return err
	}
	return tx.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "term_id"}, {Name: "day"}}, DoUpdates: clause.Assignments(map[string]any{
		"uploaded": gorm.Expr("fleet_term_usage_days.uploaded + EXCLUDED.uploaded"), "downloaded": gorm.Expr("fleet_term_usage_days.downloaded + EXCLUDED.downloaded"),
	})}).CreateInBatches(history, 500).Error
}
