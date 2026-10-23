package service

import (
	"errors"
	"math"
	"slices"
	"time"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/core"
)

var ErrFleetUsage = errors.New("fleet traffic projection requires reconciliation")

func fleetFreshUsageProof(version int64, current, pending *string, history bool) (bool, error) {
	if current != nil {
		return false, nil
	}
	if pending != nil || history {
		return false, ErrFleetUsage
	}
	return version == 1, nil
}

type fleetUsageRow struct {
	ClientID                                                 string
	Email                                                    string
	Enable                                                   bool
	Expiry, Quota                                            int64
	Reset                                                    int
	CurrentTermID                                            *string
	PendingTermID                                            *string
	CredentialClaimsVersion                                  int64
	HasTerms                                                 bool
	UsageTermID                                              *string
	BudgetID                                                 *string
	SeedID                                                   *int
	State                                                    string
	Unlimited                                                bool
	Uploaded, Downloaded, OpeningUploaded, OpeningDownloaded int64
	Reserved, Received, Observed                             int64
	SeedUploaded, SeedDownloaded, SeedLastOnline             int64
	TermExpiry, TermQuota                                    int64
	BudgetUsed                                               int64
}

func projectFleetTraffic(db *gorm.DB, emails []string, destination map[string]*core.ClientTraffic) error {
	query := db.Table("fleet_clients fc").Select(`fc.id AS client_id, c.email, c.enable, c.expiry_time AS expiry, c.total_gb AS quota, c.reset,
fc.current_term_id, fc.pending_term_id, fc.credential_claims_version,
EXISTS (SELECT 1 FROM fleet_terms history WHERE history.client_id=fc.id) AS has_terms,
u.term_id AS usage_term_id, b.id AS budget_id, t.state, t.opening_uploaded, t.opening_downloaded,
u.uploaded, u.downloaded, u.received_unix_milli AS received, u.observed_unix_milli AS observed,
b.unlimited, b.reserved_bytes AS reserved, b.used_bytes AS budget_used, t.expires_unix_milli AS term_expiry, b.limit_bytes AS term_quota,
seed.id AS seed_id, seed.up AS seed_uploaded, seed.down AS seed_downloaded, seed.last_online AS seed_last_online`).Joins("JOIN clients c ON c.id = fc.panel_client_id").
		Joins("LEFT JOIN fleet_terms t ON t.id = fc.current_term_id").Joins("LEFT JOIN fleet_term_usages u ON u.term_id = t.id").
		Joins("LEFT JOIN fleet_budgets b ON b.id = t.budget_id").Joins("LEFT JOIN client_traffics seed ON seed.email = c.email").Where("fc.owned")
	chunks := chunkStrings(emails, sqlInChunk)
	if emails == nil {
		chunks = [][]string{nil}
	}
	for _, chunk := range chunks {
		var rows []fleetUsageRow
		tx := query.Session(&gorm.Session{})
		if chunk != nil {
			tx = tx.Where("c.email IN ?", chunk)
		}
		if err := tx.Order("c.id").Scan(&rows).Error; err != nil {
			return err
		}
		for _, row := range rows {
			traffic := destination[row.Email]
			if traffic == nil {
				traffic = &core.ClientTraffic{Email: row.Email}
				destination[row.Email] = traffic
			}
			traffic.Enable, traffic.ExpiryTime, traffic.Total, traffic.Reset = row.Enable, row.Expiry, row.Quota, row.Reset
			accounting := &core.TrafficAccounting{Source: "fleet", ClientID: row.ClientID, State: "awaiting_activation", HistoryGranularity: "day", HistoryBasis: "reported"}
			if row.CurrentTermID != nil {
				if row.UsageTermID == nil || row.BudgetID == nil || row.OpeningUploaded > math.MaxInt64-row.Uploaded || row.OpeningDownloaded > math.MaxInt64-row.Downloaded {
					return ErrFleetUsage
				}
				traffic.Up, traffic.Down, traffic.LastOnline = row.OpeningUploaded+row.Uploaded, row.OpeningDownloaded+row.Downloaded, row.Observed
				if traffic.Up > math.MaxInt64-traffic.Down || traffic.Up+traffic.Down != row.BudgetUsed {
					return ErrFleetUsage
				}
				traffic.ExpiryTime, traffic.Total = row.TermExpiry, row.TermQuota
				if row.Unlimited {
					traffic.Total = 0
				}
				accounting.TermID, accounting.State = *row.CurrentTermID, row.State
				accounting.OpeningUploaded, accounting.OpeningDownloaded = row.OpeningUploaded, row.OpeningDownloaded
				accounting.MeasuredUploaded, accounting.MeasuredDownloaded = row.Uploaded, row.Downloaded
				accounting.ReportedUnixMilli, accounting.ObservedUnixMilli = row.Received, row.Observed
				if !row.Unlimited {
					value := row.Reserved
					accounting.ReservedBytes = &value
				}
			} else {
				fresh, err := fleetFreshUsageProof(row.CredentialClaimsVersion, row.CurrentTermID, row.PendingTermID, row.HasTerms)
				if err != nil {
					return err
				}
				if fresh {
					traffic.Up, traffic.Down, traffic.LastOnline = 0, 0, 0
				} else {
					if row.SeedID == nil {
						return ErrFleetUsage
					}
					traffic.Up, traffic.Down, traffic.LastOnline = row.SeedUploaded, row.SeedDownloaded, row.SeedLastOnline
					accounting.OpeningUploaded, accounting.OpeningDownloaded = row.SeedUploaded, row.SeedDownloaded
				}
			}
			traffic.Accounting = accounting
		}
	}
	return nil
}

func withFleetTraffic(db *gorm.DB, rows []*core.ClientTraffic, emails []string) ([]*core.ClientTraffic, error) {
	destination := make(map[string]*core.ClientTraffic, len(rows))
	for _, row := range rows {
		if row != nil {
			destination[row.Email] = row
		}
	}
	if err := projectFleetTraffic(db, emails, destination); err != nil {
		return nil, err
	}
	result := make([]*core.ClientTraffic, 0, len(destination))
	for _, row := range destination {
		result = append(result, row)
	}
	slices.SortFunc(result, func(a, b *core.ClientTraffic) int {
		if a.Email < b.Email {
			return -1
		}
		if a.Email > b.Email {
			return 1
		}
		return 0
	})
	return result, nil
}

func projectFleetHistories(db *gorm.DB, emails []string, now time.Time, destination map[string][]ClientHistoryPoint) error {
	for _, chunk := range chunkStrings(emails, sqlInChunk) {
		var rows []struct {
			Email                string
			Day                  *int64
			Uploaded, Downloaded int64
		}
		if err := db.Table("fleet_clients fc").Select("c.email, h.day, SUM(h.uploaded) AS uploaded, SUM(h.downloaded) AS downloaded").
			Joins("JOIN clients c ON c.id = fc.panel_client_id").Joins("LEFT JOIN fleet_terms t ON t.client_id = fc.id").
			Joins("LEFT JOIN fleet_term_usage_days h ON h.term_id = t.id AND h.day >= ?", now.Unix()/86400*86400-13*86400).
			Where("fc.owned AND c.email IN ?", chunk).Group("c.email, h.day").Order("c.email, h.day").Scan(&rows).Error; err != nil {
			return err
		}
		seen := make(map[string]bool, len(chunk))
		for _, row := range rows {
			if !seen[row.Email] {
				delete(destination, row.Email)
				seen[row.Email] = true
			}
			if row.Day != nil {
				destination[row.Email] = append(destination[row.Email], ClientHistoryPoint{T: *row.Day, Up: row.Uploaded, Down: row.Downloaded})
			}
		}
	}
	return nil
}
