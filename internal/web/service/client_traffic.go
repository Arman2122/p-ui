package service

import (
	"context"
	"strings"
	"time"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/util/common"
	"github.com/Arman2122/p-ui/internal/xray"

	"gorm.io/gorm"
)

// ResellerClientEnabledSQL binds now in milliseconds and is scoped to client_traffics.
func ResellerClientEnabledSQL() string {
	return `NOT EXISTS (SELECT 1 FROM clients owner_client JOIN reseller_accounts owner_account
		ON owner_account.user_id = owner_client.owner_user_id WHERE owner_client.email = client_traffics.email
		AND (owner_account.suspended OR owner_account.budget_blocked
		OR (owner_account.expires_at > 0 AND owner_account.expires_at <= ?)
		OR (owner_account.traffic_limit > 0 AND owner_account.traffic_used >= owner_account.traffic_limit)))`
}

/*
A client is disabled in three places and the core reads all of them: the
client_traffics row (both render paths), the clients row (RenderInbound, so every
Xray kind) and the inbound's settings JSON (buildInboundForLocalRuntime, so
mtproto and wgkernel). The depletion pass writes all three; a reset that restores
one leaves the client cut off exactly as before, with the panel showing a fresh
allowance.

These two functions are the before and after of a reset. The reset paths write
client_traffics directly for bulk efficiency — one UPDATE over N emails instead
of N Update calls — and that shortcut is how they came to restore one location
out of three.
*/

/*
depletedAndDisabled names the clients a depletion pass had cut off, which is the
only set a reset may bring back: enable=false also means admin-disabled, and
zeroing usage is not a reason to undo that.

A client that is BOTH admin-disabled and depleted does match here, and does come
back. Deliberate, and the same answer the per-client paths already give — they
call Update with Enable=true unconditionally. A reset is the operator saying
"give this client its allowance back", and the panel has never split the two
reasons inside that gesture.

Pass a nil email list for "every client", which is what the panel-wide reset
means.
*/
func depletedAndDisabled(tx *gorm.DB, emails []string) ([]string, error) {
	cond, args := depletedCond(tx)
	query := func(extra string, extraArgs ...any) ([]string, error) {
		var rows []string
		err := tx.Model(xray.ClientTraffic{}).
			Where(cond+" AND enable = ?"+extra, append(append(append([]any{}, args...), false), extraArgs...)...).
			Pluck("email", &rows).Error
		return rows, err
	}
	if emails == nil {
		return query("")
	}
	out := make([]string, 0, len(emails))
	for _, batch := range chunkStrings(emails, sqlInChunk) {
		rows, err := query(" AND email IN ?", batch)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	return out, nil
}

/*
reEnableRecovered drives the clients that are no longer depleted back through
BulkSetEnable — the one write path that owns all three enable locations and the
runtime push — so the reset paths do not have to know where those locations are.

Re-checked rather than assumed: zeroing usage does not clear an expiry-based
depletion, so a client past its expiryTime stays disabled, and should.

Call it after the traffic write has committed, never inside submitTrafficWrite:
that queue is drained by a single worker.
*/
func (s *ClientService) reEnableRecovered(inboundSvc *InboundService, was []string) (bool, error) {
	if len(was) == 0 || inboundSvc == nil {
		return false, nil
	}
	db := database.GetDB()
	cond, args := depletedCond(db)
	cond = "(" + cond + " OR NOT (" + ResellerClientEnabledSQL() + "))"
	args = append(args, time.Now().UnixMilli())
	stillDepleted := map[string]struct{}{}
	for _, batch := range chunkStrings(was, sqlInChunk) {
		var rows []string
		if err := db.Model(xray.ClientTraffic{}).
			Where(cond+" AND email IN ?", append(append([]any{}, args...), batch)...).
			Pluck("email", &rows).Error; err != nil {
			return false, err
		}
		for _, email := range rows {
			stillDepleted[email] = struct{}{}
		}
	}
	recovered := make([]string, 0, len(was))
	for _, email := range was {
		if _, depleted := stillDepleted[email]; !depleted {
			recovered = append(recovered, email)
		}
	}
	if len(recovered) == 0 {
		return false, nil
	}
	_, needRestart, err := s.BulkSetEnable(inboundSvc, recovered, true)
	return needRestart, err
}

func (s *ClientService) ResetTrafficByEmail(inboundSvc *InboundService, email string) (bool, error) {
	if email == "" {
		return false, common.NewError("client email is required")
	}
	rec, err := s.GetRecordByEmail(nil, email)
	if err != nil {
		return false, err
	}
	inboundIds, err := s.GetInboundIdsForRecord(rec.Id)
	if err != nil {
		return false, err
	}

	needRestart := false
	if len(inboundIds) == 0 {
		if rErr := inboundSvc.ResetClientTrafficByEmail(email); rErr != nil {
			return needRestart, rErr
		}
	}

	for _, ibId := range inboundIds {
		var nr bool
		rErr := submitTrafficWrite(func() error {
			var err error
			nr, err = inboundSvc.resetClientTrafficLocked(ibId, email)
			return err
		})
		needRestart = needRestart || nr
		if rErr != nil {
			return needRestart, rErr
		}
	}
	// Zero usage before enabling so a concurrent traffic tick cannot disable it again.
	expiredDetached := len(inboundIds) == 0 && rec.ExpiryTime > 0 && rec.ExpiryTime <= time.Now().UnixMilli()
	if !rec.Enable && !expiredDetached {
		var ownerAllowsEnable bool
		// Detached clients may have no traffic row; eligibility follows their client record.
		if err := database.GetDB().Table("clients AS client_traffics").
			Select(ResellerClientEnabledSQL(), time.Now().UnixMilli()).
			Where("email = ?", email).Scan(&ownerAllowsEnable).Error; err != nil {
			return false, err
		}
		if ownerAllowsEnable {
			updated := rec.ToClient()
			updated.Enable = true
			if hErr := hydrateClientCredentials(nil, rec.Id, 0, updated); hErr != nil {
				return false, hErr
			}
			nr, uErr := s.Update(inboundSvc, rec.Id, *updated)
			needRestart = needRestart || nr
			if uErr != nil {
				logger.Warning("Failed to auto-enable client during traffic reset:", uErr)
				return needRestart, uErr
			}
		}
	}

	if len(inboundIds) > 0 {
		if err := resetCoreQuotas(context.Background(), []string{email}); err != nil {
			return needRestart, err
		}
		for _, id := range inboundIds {
			if err := markInboundTrafficReset(id); err != nil {
				return needRestart, err
			}
		}
	}
	return needRestart, nil
}

func (s *ClientService) BulkResetTraffic(inboundSvc *InboundService, emails []string) (int, error) {
	if len(emails) == 0 {
		return 0, nil
	}
	seen := map[string]struct{}{}
	cleanEmails := make([]string, 0, len(emails))
	for _, e := range emails {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if _, ok := seen[e]; ok {
			continue
		}
		seen[e] = struct{}{}
		cleanEmails = append(cleanEmails, e)
	}
	if len(cleanEmails) == 0 {
		return 0, nil
	}

	affected := 0
	err := submitTrafficWrite(func() error {
		db := database.GetDB()
		return db.Transaction(func(tx *gorm.DB) error {
			if err := requireLegacyClientLifecycle(tx, cleanEmails); err != nil {
				return err
			}
			if err := CaptureClientHistoryBeforeReset(tx, cleanEmails, time.Now()); err != nil {
				return err
			}
			if err := adjustGroupBaselinesForRemovedTraffic(tx, cleanEmails); err != nil {
				return err
			}
			for _, batch := range chunkStrings(cleanEmails, sqlInChunk) {
				res := tx.Model(xray.ClientTraffic{}).
					Where("email IN ?", batch).
					Updates(map[string]any{"enable": gorm.Expr(ResellerClientEnabledSQL(), time.Now().UnixMilli()), "up": 0, "down": 0})
				if res.Error != nil {
					return res.Error
				}
				affected += int(res.RowsAffected)
			}
			if err := clearGlobalTraffic(tx, cleanEmails...); err != nil {
				return err
			}
			for _, batch := range chunkStrings(cleanEmails, sqlInChunk) {
				if err := tx.Model(&model.NodeClientTraffic{}).Where("email IN ?", batch).Updates(map[string]any{"reset_pending": true, "reset_after_count": gorm.Expr("COALESCE(observed_reset_count, reset_count, 0)")}).Error; err != nil {
					return err
				}
			}
			return nil
		})
	})
	if err != nil {
		return 0, err
	}
	for _, e := range cleanEmails {
		rec, err := s.GetRecordByEmail(nil, e)
		if err == nil && !rec.Enable {
			var ownerAllowsEnable bool
			if err := database.GetDB().Model(&xray.ClientTraffic{}).
				Select(ResellerClientEnabledSQL(), time.Now().UnixMilli()).
				Where("email = ?", e).Scan(&ownerAllowsEnable).Error; err != nil {
				return 0, err
			}
			if !ownerAllowsEnable {
				continue
			}
			updated := rec.ToClient()
			updated.Enable = true
			if hErr := hydrateClientCredentials(nil, rec.Id, 0, updated); hErr != nil {
				logger.Warning("Failed to read stored credentials during bulk traffic reset:", hErr)
				return 0, hErr
			}
			nr, uErr := s.Update(inboundSvc, rec.Id, *updated)
			if nr {
				(&XrayService{}).SetToNeedRestart()
			}
			if uErr != nil {
				logger.Warning("Failed to auto-enable client during bulk traffic reset:", uErr)
				return 0, uErr
			}
		}
	}

	// The sidecar counts for itself, so a panel-side zero means nothing to it:
	// an mtproto client stays blocked on its own quota until this call.
	return affected, resetCoreQuotas(context.Background(), cleanEmails)
}

func (s *ClientService) ResetAllClientTraffics(inboundSvc *InboundService, id int) error {
	var wasDepleted, resetEmails []string
	err := submitTrafficWrite(func() error {
		var lockedErr error
		wasDepleted, resetEmails, lockedErr = s.resetAllClientTrafficsLocked(id)
		return lockedErr
	})
	if err != nil {
		return err
	}
	needRestart, err := s.reEnableRecovered(inboundSvc, wasDepleted)
	if needRestart {
		(&XrayService{}).SetToNeedRestart()
	}
	if err != nil {
		return err
	}
	if err := resetCoreQuotas(context.Background(), resetEmails); err != nil {
		return err
	}
	return markInboundTrafficReset(id)
}

func (s *ClientService) resetAllClientTrafficsLocked(id int) ([]string, []string, error) {
	db := database.GetDB()
	var wasDepleted, resetEmails []string

	if err := db.Transaction(func(tx *gorm.DB) error {
		// client_traffics.inbound_id is stale: it reflects the inbound the row was
		// first inserted under and is never refreshed. Use the client_inbounds join
		// as the authoritative source for which emails belong to a given inbound.
		if id == -1 {
			if err := tx.Model(xray.ClientTraffic{}).Pluck("email", &resetEmails).Error; err != nil {
				return err
			}
		} else {
			if err := tx.Table("client_inbounds ci").
				Select("c.email").
				Joins("JOIN clients c ON c.id = ci.client_id").
				Where("ci.inbound_id = ?", id).
				Pluck("c.email", &resetEmails).Error; err != nil {
				return err
			}
		}
		if len(resetEmails) == 0 {
			return nil
		}
		if err := requireLegacyClientLifecycle(tx, resetEmails); err != nil {
			return err
		}

		if err := CaptureClientHistoryBeforeReset(tx, resetEmails, time.Now()); err != nil {
			return err
		}
		if err := adjustGroupBaselinesForRemovedTraffic(tx, resetEmails); err != nil {
			return err
		}

		// Before the usage is zeroed, while depletion is still readable off the row.
		var snapErr error
		if wasDepleted, snapErr = depletedAndDisabled(tx, resetEmails); snapErr != nil {
			return snapErr
		}

		result := tx.Model(xray.ClientTraffic{}).
			Where("email IN ?", resetEmails).
			Updates(map[string]any{"up": 0, "down": 0})

		if result.Error != nil {
			return result.Error
		}

		if err := clearGlobalTraffic(tx, resetEmails...); err != nil {
			return err
		}

		for _, batch := range chunkStrings(resetEmails, sqlInChunk) {
			if err := tx.Model(&model.NodeClientTraffic{}).Where("email IN ?", batch).Updates(map[string]any{"reset_pending": true, "reset_after_count": gorm.Expr("COALESCE(observed_reset_count, reset_count, 0)")}).Error; err != nil {
				return err
			}
		}

		return nil
	}); err != nil {
		return nil, nil, err
	}
	return wasDepleted, resetEmails, nil
}

func markInboundTrafficReset(id int) error {
	return submitTrafficWrite(func() error {
		query := database.GetDB().Model(&model.Inbound{})
		if id == -1 {
			query = query.Where("id > ?", id)
		} else {
			query = query.Where("id = ?", id)
		}
		return query.Update("last_traffic_reset_time", time.Now().UnixMilli()).Error
	})
}

func (s *ClientService) ResetAllTraffics(inboundSvc *InboundService) (bool, error) {
	var affected int64
	var wasDepleted []string
	err := submitTrafficWrite(func() error {
		return database.GetDB().Transaction(func(tx *gorm.DB) error {
			var resetEmails []string
			if err := tx.Model(&xray.ClientTraffic{}).Pluck("email", &resetEmails).Error; err != nil {
				return err
			}
			if err := requireLegacyClientLifecycle(tx, resetEmails); err != nil {
				return err
			}
			if err := CaptureClientHistoryBeforeReset(tx, resetEmails, time.Now()); err != nil {
				return err
			}
			if err := adjustGroupBaselinesForRemovedTraffic(tx, resetEmails); err != nil {
				return err
			}
			var snapErr error
			if wasDepleted, snapErr = depletedAndDisabled(tx, nil); snapErr != nil {
				return snapErr
			}
			res := tx.Model(&xray.ClientTraffic{}).
				Where("1 = 1").
				Updates(map[string]any{"up": 0, "down": 0})
			if res.Error != nil {
				return res.Error
			}
			affected = res.RowsAffected
			if err := tx.Where("1 = 1").Delete(&model.ClientGlobalTraffic{}).Error; err != nil {
				return err
			}
			return tx.Model(&model.NodeClientTraffic{}).Where("1 = 1").Updates(map[string]any{"reset_pending": true, "reset_after_count": gorm.Expr("COALESCE(observed_reset_count, reset_count, 0)")}).Error
		})
	})
	if err != nil {
		return false, err
	}
	needRestart, err := s.reEnableRecovered(inboundSvc, wasDepleted)
	if err != nil {
		return needRestart, err
	}
	return needRestart || affected > 0, resetCoreQuotasForAll(context.Background())
}

// BulkResetTrafficResult is the per-row account of a bulk reset: the clients
// whose usage was zeroed, and the emails that name no client.
type BulkResetTrafficResult struct {
	Affected int         `json:"affected" example:"2"`
	Outcome  BulkOutcome `json:"outcome"`
}

// BulkResetTrafficOutcome resets like BulkResetTraffic and says per row what
// happened; the count alone could not tell an unknown email from a reset one.
func (s *ClientService) BulkResetTrafficOutcome(inboundSvc *InboundService, emails []string) (*BulkResetTrafficResult, error) {
	result := &BulkResetTrafficResult{}
	known := map[string]struct{}{}
	for _, batch := range chunkStrings(emails, sqlInChunk) {
		var found []string
		if err := database.GetDB().Model(&model.ClientRecord{}).Where("email IN ?", batch).Pluck("email", &found).Error; err != nil {
			return nil, err
		}
		for _, email := range found {
			known[email] = struct{}{}
		}
	}
	affected, err := s.BulkResetTraffic(inboundSvc, emails)
	if err != nil {
		return nil, err
	}
	result.Affected = affected
	seen := map[string]struct{}{}
	for _, email := range emails {
		email = strings.TrimSpace(email)
		if email == "" {
			continue
		}
		if _, dup := seen[email]; dup {
			continue
		}
		seen[email] = struct{}{}
		if _, ok := known[email]; ok {
			result.Outcome.ok(email)
		} else {
			result.Outcome.skip(email, "client not found")
		}
	}
	result.Outcome.finalize()
	return result, nil
}
