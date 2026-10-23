package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/eventbus"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/policy"
	"github.com/Arman2122/p-ui/internal/web/session"
)

// ResellerNotice is one account transition; the activity feed and the event bus both carry it.
type ResellerNotice struct {
	UserID          int    `json:"userId" example:"2"`
	Username        string `json:"username" example:"sina"`
	Name            string `json:"name" example:"Sina M."`
	Kind            string `json:"kind" example:"depleted"`
	UsedBytes       int64  `json:"usedBytes" example:"536870912000"`
	LimitBytes      int64  `json:"limitBytes" example:"536870912000"`
	ExpiresAt       int64  `json:"expiresAt" example:"1800000000000"`
	ClientsDisabled int    `json:"clientsDisabled" example:"12"`
	At              int64  `json:"at" example:"1790000000000"`
}

const (
	resellerExpiryWarning  = 7 * 24 * time.Hour
	resellerNoticeWarning  = "warning"
	resellerNoticeDepleted = "depleted"
	resellerNoticeExpiring = "expiring"
	resellerNoticeExpired  = "expired"
	resellerNoticeRestored = "restored"
	resellerNoticeReset    = "reset"
)

// The activity feed reads formatted values; a raw byte count is not a sentence.
func FormatGiB(bytes int64) string {
	return strconv.FormatFloat(float64(bytes)/(1<<30), 'f', 1, 64)
}

func (n ResellerNotice) daysLeft() int {
	if n.ExpiresAt <= 0 {
		return 0
	}
	return int(math.Ceil(float64(n.ExpiresAt-n.At) / 86400000))
}

// ActivityKeys names the feed entry for this transition and how loudly it should read.
func (n ResellerNotice) ActivityKeys() (title, body, level string) {
	switch n.Kind {
	case resellerNoticeWarning:
		return "events.resellerWarning.title", "events.resellerWarning.body", "warning"
	case resellerNoticeDepleted:
		return "events.resellerDepleted.title", "events.resellerDepleted.body", "error"
	case resellerNoticeExpiring:
		return "events.resellerExpiring.title", "events.resellerExpiring.body", "warning"
	case resellerNoticeExpired:
		return "events.resellerExpired.title", "events.resellerExpired.body", "error"
	case resellerNoticeRestored:
		return "events.resellerRestored.title", "events.resellerRestored.body", "success"
	case resellerNoticeReset:
		return "events.resellerReset.title", "events.resellerReset.body", "info"
	}
	return "", "", ""
}

func (n ResellerNotice) ActivityParams() map[string]string {
	return map[string]string{
		"name": n.Name, "username": n.Username, "used": FormatGiB(n.UsedBytes), "limit": FormatGiB(n.LimitBytes),
		"clients": strconv.Itoa(n.ClientsDisabled), "days": strconv.Itoa(n.daysLeft()),
	}
}

// BotMessage is the Telegram and email rendering: one template per transition, same parameters.
func (n ResellerNotice) BotMessage() (key string, params []string) {
	switch n.Kind {
	case resellerNoticeWarning:
		key = "tgbot.messages.eventResellerWarning"
	case resellerNoticeDepleted:
		key = "tgbot.messages.eventResellerDepleted"
	case resellerNoticeExpiring:
		key = "tgbot.messages.eventResellerExpiring"
	case resellerNoticeExpired:
		key = "tgbot.messages.eventResellerExpired"
	case resellerNoticeRestored:
		key = "tgbot.messages.eventResellerRestored"
	case resellerNoticeReset:
		key = "tgbot.messages.eventResellerReset"
	default:
		return "", nil
	}
	return key, []string{
		"Name==" + n.Name, "Username==" + n.Username, "Used==" + FormatGiB(n.UsedBytes), "Limit==" + FormatGiB(n.LimitBytes),
		"Clients==" + strconv.Itoa(n.ClientsDisabled), "Days==" + strconv.Itoa(n.daysLeft()),
	}
}

// resellerNeedsPass keeps the per-tick lock to accounts with something to converge or announce.
func resellerNeedsPass(a *model.ResellerAccount, now time.Time) bool {
	ms := now.UnixMilli()
	over := a.TrafficLimit > 0 && a.TrafficUsed >= a.TrafficLimit
	expired := a.ExpiresAt > 0 && a.ExpiresAt <= ms
	return a.Suspended || a.BudgetBlocked || over || expired || resellerResetDue(a, now) ||
		resellerTrafficNear(a) != (a.TrafficWarnedAt > 0) || resellerExpiryNear(a, ms) != (a.ExpiryWarnedAt > 0)
}

func resellerNextReset(a *model.ResellerAccount) int64 {
	anchor := a.PeriodStartedAt
	if anchor == 0 {
		anchor = a.CreatedAt
	}
	if a.ResetCycle == "none" || anchor == 0 {
		return 0
	}
	next := policy.NextReset(time.UnixMilli(anchor), a.ResetCycle)
	if next.IsZero() {
		return 0
	}
	return next.UnixMilli()
}

func resellerResetDue(a *model.ResellerAccount, now time.Time) bool {
	next := resellerNextReset(a)
	return next > 0 && next <= now.UnixMilli()
}

// resetUsageLocked opens a new period: the counter is zeroed, the ledger and per-client cursors stay.
func (s *ResellerService) resetUsageLocked(a *model.ResellerAccount, now time.Time) error {
	updates := map[string]any{"traffic_used": 0, "period_started_at": now.UnixMilli(), "traffic_warned_at": 0}
	if err := submitTrafficWrite(func() error {
		return database.GetDB().Transaction(func(tx *gorm.DB) error {
			if err := requireLegacyPool(tx, &a.UserID, ""); err != nil {
				return err
			}
			return tx.Model(&model.ResellerAccount{}).Where("user_id = ?", a.UserID).Updates(updates).Error
		})
	}); err != nil {
		return err
	}
	a.TrafficUsed, a.PeriodStartedAt, a.TrafficWarnedAt = 0, now.UnixMilli(), 0
	s.publish([]ResellerNotice{s.notice(a, resellerNoticeReset, 0, now)})
	return nil
}

func (s *ResellerService) ResetUsage(user *model.User, id int) error {
	if !session.IsAdminUser(user) {
		return ErrResellerForbidden
	}
	return s.WithAccountLock(id, func() error {
		now := time.Now()
		var a model.ResellerAccount
		if err := database.GetDB().First(&a, "user_id = ?", id).Error; err != nil {
			return err
		}
		if err := s.resetUsageLocked(&a, now); err != nil {
			return err
		}
		_, err := s.convergeBudgetLocked(id, now)
		return err
	})
}

func resellerTrafficNear(a *model.ResellerAccount) bool {
	return a.TrafficLimit > 0 && a.TrafficUsed < a.TrafficLimit && a.TrafficUsed >= a.TrafficLimit-a.TrafficLimit/10
}

func resellerExpiryNear(a *model.ResellerAccount, nowMs int64) bool {
	return a.ExpiresAt > nowMs && a.ExpiresAt-nowMs <= resellerExpiryWarning.Milliseconds()
}

// EnforceBudgets returns the clients it switched off so the caller can cut their live sessions.
// Notices are published here; the two schedulers that call this must not publish again.
func (s *ResellerService) EnforceBudgets(ctx context.Context) ([]string, error) {
	var accounts []model.ResellerAccount
	if err := database.GetDB().WithContext(ctx).Order("user_id").Find(&accounts).Error; err != nil {
		return nil, err
	}
	now := time.Now()
	var disabled []string
	var failures []error
	for i := range accounts {
		if !resellerNeedsPass(&accounts[i], now) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return disabled, err
		}
		id := accounts[i].UserID
		err := s.WithAccountLock(id, func() error {
			emails, err := s.convergeBudgetLocked(id, now)
			disabled = append(disabled, emails...)
			return err
		})
		if err != nil {
			failures = append(failures, err)
		}
	}
	return disabled, errors.Join(failures...)
}

// convergeBudgetLocked pauses or restores owned clients and announces each transition once.
// Every write runs on its own connection: the lock transaction must never hold a client row.
func (s *ResellerService) convergeBudgetLocked(id int, now time.Time) ([]string, error) {
	db := database.GetDB()
	var a model.ResellerAccount
	if err := db.First(&a, "user_id = ?", id).Error; err != nil {
		return nil, err
	}
	if resellerResetDue(&a, now) {
		if err := s.resetUsageLocked(&a, now); err != nil {
			return nil, err
		}
	}
	ms := now.UnixMilli()
	over := a.TrafficLimit > 0 && a.TrafficUsed >= a.TrafficLimit
	expired := a.ExpiresAt > 0 && a.ExpiresAt <= ms
	blocked := over || expired
	// A lapsed term or a suspension always cuts; only a traffic depletion honours the soft policy.
	disable := expired || a.Suspended || over && a.OnDepletion != "block"
	if disable {
		if err := db.Transaction(func(tx *gorm.DB) error {
			var ids []int
			if err := tx.Model(&model.ClientRecord{}).Where("owner_user_id = ? AND enable = ?", id, true).Pluck("id", &ids).Error; err != nil {
				return err
			}
			for _, cid := range ids {
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.ResellerPausedClient{UserID: id, ClientID: cid}).Error; err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	// The flag flips before any client does: re-enabling reads it as the owner gate.
	var notices []ResellerNotice
	if blocked != a.BudgetBlocked {
		res := db.Model(&model.ResellerAccount{}).Where("user_id = ? AND budget_blocked = ?", id, a.BudgetBlocked).Update("budget_blocked", blocked)
		if res.Error != nil {
			return nil, res.Error
		}
		if res.RowsAffected == 1 {
			kind := resellerNoticeRestored
			var paused int64
			if blocked {
				kind = resellerNoticeDepleted
				if !over {
					kind = resellerNoticeExpired
				}
				if err := db.Model(&model.ResellerPausedClient{}).Where("user_id = ?", id).Count(&paused).Error; err != nil {
					return nil, err
				}
			}
			notices = append(notices, s.notice(&a, kind, int(paused), now))
		}
	}
	notice, err := s.settleWarning(db, &a, "traffic_warned_at", a.TrafficWarnedAt, resellerTrafficNear(&a), resellerNoticeWarning, now)
	if err != nil {
		return nil, err
	}
	notices = append(notices, notice...)
	notice, err = s.settleWarning(db, &a, "expiry_warned_at", a.ExpiryWarnedAt, resellerExpiryNear(&a, ms), resellerNoticeExpiring, now)
	if err != nil {
		return nil, err
	}
	notices = append(notices, notice...)
	s.publish(notices)
	// With no reason left to pause, what an earlier pass paused comes back even while the
	// soft policy still blocks provisioning: block means the clients stay online.
	var recs []model.ClientRecord
	if err := db.Where("owner_user_id = ? AND id IN (?)", id, db.Model(&model.ResellerPausedClient{}).Select("client_id").Where("user_id = ?", id)).Find(&recs).Error; err != nil {
		return nil, err
	}
	var disabled []string
	for _, rec := range recs {
		// Update commits remote-node dirtiness before dispatch; node reconciliation retries failures.
		// Local failures request the existing runtime restart/convergence path.
		if rec.Enable == !disable {
			continue
		}
		c := rec.ToClient()
		c.Enable = !disable
		if !disable && (rec.ExpiryTime > 0 && rec.ExpiryTime <= ms) {
			continue
		}
		var nr bool
		if disable {
			nr, err = (&ClientService{}).Update(&InboundService{}, rec.Id, *c)
		} else {
			nr, err = (&ClientService{}).restoreOwned(&InboundService{}, rec.Id, *c)
		}
		resellerApplyPending(nr, false)
		if err != nil {
			return disabled, fmt.Errorf("reseller %d client %d requires retry: %w", id, rec.Id, err)
		}
		if disable {
			disabled = append(disabled, rec.Email)
		}
	}
	if !disable {
		return disabled, db.Where("user_id = ?", id).Delete(&model.ResellerPausedClient{}).Error
	}
	return disabled, nil
}

// settleWarning raises a threshold notice once and forgets it when the account leaves the band.
func (s *ResellerService) settleWarning(db *gorm.DB, a *model.ResellerAccount, column string, warnedAt int64, near bool, kind string, now time.Time) ([]ResellerNotice, error) {
	switch {
	case near && warnedAt == 0:
		res := db.Model(&model.ResellerAccount{}).Where("user_id = ? AND "+column+" = 0", a.UserID).Update(column, now.UnixMilli())
		if res.Error != nil {
			return nil, res.Error
		}
		if res.RowsAffected == 1 {
			return []ResellerNotice{s.notice(a, kind, 0, now)}, nil
		}
	case !near && warnedAt > 0:
		return nil, db.Model(&model.ResellerAccount{}).Where("user_id = ?", a.UserID).Update(column, 0).Error
	}
	return nil, nil
}

func (s *ResellerService) notice(a *model.ResellerAccount, kind string, paused int, now time.Time) ResellerNotice {
	var u model.User
	if err := database.GetDB().Select("username").First(&u, a.UserID).Error; err != nil {
		logger.Warning("reseller notice without a login handle:", err)
	}
	return ResellerNotice{
		UserID: a.UserID, Username: u.Username, Name: a.Name, Kind: kind, UsedBytes: a.TrafficUsed, LimitBytes: a.TrafficLimit,
		ExpiresAt: a.ExpiresAt, ClientsDisabled: paused, At: now.UnixMilli(),
	}
}

// publish writes the transition to the reseller's feed, which administrators read as well,
// and hands it to the notifiers.
func (s *ResellerService) publish(notices []ResellerNotice) {
	for _, n := range notices {
		title, body, level := n.ActivityKeys()
		if title == "" {
			continue
		}
		row := model.ActivityEvent{AudienceUserID: n.UserID, Kind: "reseller." + n.Kind, Level: level, TitleKey: title, MessageKey: body, CreatedAt: n.At}
		if _, err := (ActivityService{}).Record(context.Background(), row, n.ActivityParams()); err != nil {
			logger.Warning("persist reseller notice failed:", err)
		}
		if eventBus != nil {
			eventBus.Publish(eventbus.Event{Type: eventbus.EventResellerBudget, Source: n.Username, Timestamp: time.UnixMilli(n.At), Data: n})
		}
	}
}

func (s *ResellerService) Suspend(user *model.User, id int, suspended bool) error {
	if !session.IsAdminUser(user) {
		return ErrResellerForbidden
	}
	var disabled []string
	err := s.WithAccountLock(id, func() error {
		err := database.GetDB().Transaction(func(tx *gorm.DB) error {
			var a model.ResellerAccount
			if err := tx.First(&a, "user_id = ?", id).Error; err != nil {
				return err
			}
			if err := tx.Model(&a).Update("suspended", suspended).Error; err != nil {
				return err
			}
			return tx.Model(&model.User{}).Where("id = ? AND role = ?", id, "reseller").Update("login_epoch", gorm.Expr("login_epoch + 1")).Error
		})
		if err != nil {
			return err
		}
		disabled, err = s.convergeBudgetLocked(id, time.Now())
		return err
	})
	if err != nil {
		return err
	}
	// The cut runs outside the lock: a process restart must not hold every other account op.
	if len(disabled) > 0 {
		CutDisabledClients(disabled, &SettingService{}, &XrayService{}, &InboundService{})
	}
	return nil
}
