package service

import (
	"time"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"

	"gorm.io/gorm"
)

type ClientHistoryPoint struct {
	T    int64 `json:"t" example:"1789171200"`
	Up   int64 `json:"up" example:"1048576"`
	Down int64 `json:"down" example:"8388608"`
}

// RecordClientHistory rolls changed counters into hourly byte totals in one batch.
// The cursor prevents double counting; a first observation only seeds its baseline.
func RecordClientHistory(now time.Time) error {
	return database.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := recordClientHistory(tx, nil, now); err != nil {
			return err
		}
		return tx.Where("t < ?", now.Unix()/3600*3600-14*86400).Delete(&model.ClientTrafficSample{}).Error
	})
}

func CaptureClientHistoryBeforeReset(tx *gorm.DB, emails []string, now time.Time) error {
	if len(emails) == 0 {
		return nil
	}
	for _, batch := range chunkStrings(emails, sqlInChunk) {
		if err := recordClientHistory(tx, batch, now); err != nil {
			return err
		}
		if err := tx.Model(&model.ClientHistoryCursor{}).Where("email IN ?", batch).
			Updates(map[string]any{"up": 0, "down": 0}).Error; err != nil {
			return err
		}
	}
	return nil
}

func recordClientHistory(tx *gorm.DB, emails []string, now time.Time) error {
	if err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || '.client_history', 0))").Error; err != nil {
		return err
	}
	scope := ""
	args := []any{}
	if len(emails) > 0 {
		scope = " AND c.email IN ?"
		args = append(args, emails)
	}
	args = append(args, now.Unix()/3600*3600)
	return tx.Exec(`WITH changed AS MATERIALIZED (
   SELECT c.email, ct.up, ct.down, h.email IS NOT NULL AS known,
   CASE WHEN ct.up >= h.up THEN ct.up - h.up ELSE ct.up END AS delta_up,
   CASE WHEN ct.down >= h.down THEN ct.down - h.down ELSE ct.down END AS delta_down
   FROM clients c JOIN client_traffics ct ON ct.email = c.email
   LEFT JOIN client_history_cursors h ON h.email = c.email
   WHERE (h.email IS NULL OR ct.up <> h.up OR ct.down <> h.down)`+scope+`
  ), samples AS (
   INSERT INTO client_traffic_samples (email, t, up, down)
   SELECT email, ?, GREATEST(delta_up, 0), GREATEST(delta_down, 0) FROM changed WHERE known
   ON CONFLICT (email, t) DO UPDATE SET
   up = client_traffic_samples.up + EXCLUDED.up, down = client_traffic_samples.down + EXCLUDED.down
  ) INSERT INTO client_history_cursors (email, up, down)
  SELECT email, up, down FROM changed
  ON CONFLICT (email) DO UPDATE SET up = EXCLUDED.up, down = EXCLUDED.down`, args...).Error
}

func readClientHistories(db *gorm.DB, emails []string, now time.Time) (map[string][]ClientHistoryPoint, error) {
	out := make(map[string][]ClientHistoryPoint, len(emails))
	if len(emails) == 0 {
		return out, nil
	}
	var rows []struct {
		Email              string
		ClientHistoryPoint `gorm:"embedded"`
	}
	since := now.Unix()/86400*86400 - 13*86400
	err := db.Table("client_traffic_samples").
		Select("email, t / 86400 * 86400 AS t, SUM(up) AS up, SUM(down) AS down").
		Where("t >= ? AND email IN ?", since, emails).
		Group("email, t / 86400").Order("t").Scan(&rows).Error
	for _, row := range rows {
		out[row.Email] = append(out[row.Email], row.ClientHistoryPoint)
	}
	return out, err
}
