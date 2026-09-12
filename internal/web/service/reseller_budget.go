package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/session"
)

func (s *ResellerService) EnforceBudgets(ctx context.Context) error {
	var ids []int
	if err := database.GetDB().WithContext(ctx).Model(&model.ResellerAccount{}).
		Where("suspended OR budget_blocked OR (traffic_limit > 0 AND traffic_used >= traffic_limit) OR (expires_at > 0 AND expires_at <= ?)", time.Now().UnixMilli()).Pluck("user_id", &ids).Error; err != nil {
		return err
	}
	var failures []error
	for _, id := range ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.WithAccountLock(id, func() error { return s.convergeBudgetLocked(id) }); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (s *ResellerService) convergeBudgetLocked(id int) error {
	db := database.GetDB()
	var a model.ResellerAccount
	if err := db.First(&a, "user_id = ?", id).Error; err != nil {
		return err
	}
	blocked := a.TrafficLimit > 0 && a.TrafficUsed >= a.TrafficLimit || a.ExpiresAt > 0 && a.ExpiresAt <= time.Now().UnixMilli()
	disable := blocked || a.Suspended
	if err := db.Transaction(func(tx *gorm.DB) error {
		if disable {
			var ids []int
			if err := tx.Model(&model.ClientRecord{}).Where("owner_user_id = ? AND enable = ?", id, true).Pluck("id", &ids).Error; err != nil {
				return err
			}
			for _, cid := range ids {
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.ResellerPausedClient{UserID: id, ClientID: cid}).Error; err != nil {
					return err
				}
			}
		}
		return tx.Model(&a).Update("budget_blocked", blocked).Error
	}); err != nil {
		return err
	}
	var recs []model.ClientRecord
	if err := db.Where("owner_user_id = ? AND id IN (?)", id, db.Model(&model.ResellerPausedClient{}).Select("client_id").Where("user_id = ?", id)).Find(&recs).Error; err != nil {
		return err
	}
	for _, rec := range recs {
		// Update commits remote-node dirtiness before dispatch; node reconciliation retries failures.
		// Local failures request the existing runtime restart/convergence path.
		if rec.Enable == !disable {
			continue
		}
		c := rec.ToClient()
		c.Enable = !disable
		if !disable && (rec.ExpiryTime > 0 && rec.ExpiryTime <= time.Now().UnixMilli()) {
			continue
		}
		nr, err := (&ClientService{}).Update(&InboundService{}, rec.Id, *c)
		resellerApplyPending(nr, false)
		if err != nil {
			return fmt.Errorf("reseller %d client %d requires retry: %w", id, rec.Id, err)
		}
	}
	if !disable {
		return db.Where("user_id = ?", id).Delete(&model.ResellerPausedClient{}).Error
	}
	return nil
}

func (s *ResellerService) Suspend(user *model.User, id int, suspended bool) error {
	if !session.IsAdminUser(user) {
		return ErrResellerForbidden
	}
	return s.WithAccountLock(id, func() error {
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
		return s.convergeBudgetLocked(id)
	})
}
