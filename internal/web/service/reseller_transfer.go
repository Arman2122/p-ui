package service

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/session"
)

// withAccountLocks nests the advisory locks in ascending order so two transfers cannot deadlock.
func (s *ResellerService) withAccountLocks(ids []int, fn func() error) error {
	ids = slices.Compact(slices.Sorted(slices.Values(ids)))
	var run func(int) error
	run = func(i int) error {
		if i == len(ids) {
			return fn()
		}
		if ids[i] <= 0 {
			return run(i + 1)
		}
		return s.WithAccountLock(ids[i], func() error { return run(i + 1) })
	}
	return run(0)
}

func uniqueClientIDs(ids []int) ([]int, error) {
	out := slices.Compact(slices.Sorted(slices.Values(ids)))
	if len(out) == 0 || len(out) > 1000 || out[0] <= 0 {
		return nil, ErrResellerInvalid
	}
	return out, nil
}

func clientOwnerIDs(ids []int) ([]int, error) {
	var owners []int
	err := database.GetDB().Model(&model.ClientRecord{}).Where("id IN ? AND owner_user_id IS NOT NULL", ids).Distinct().Pluck("owner_user_id", &owners).Error
	return owners, err
}

func resellerAccepting(a *model.ResellerAccount) error {
	if a.Suspended || a.BudgetBlocked || a.ExpiresAt > 0 && a.ExpiresAt <= time.Now().UnixMilli() {
		return ErrResellerBudget
	}
	return nil
}

// AssignClients moves clients under a reseller from the administrator or another reseller.
func (s *ResellerService) AssignClients(user *model.User, id int, clientIDs []int) (*ResellerTransferResult, error) {
	if !session.IsAdminUser(user) {
		return nil, ErrResellerForbidden
	}
	ids, err := uniqueClientIDs(clientIDs)
	if err != nil {
		return nil, err
	}
	owners, err := clientOwnerIDs(ids)
	if err != nil {
		return nil, err
	}
	var out *ResellerTransferResult
	err = s.withAccountLocks(append(owners, id), func() error {
		var a model.ResellerAccount
		if err := database.GetDB().First(&a, "user_id = ?", id).Error; err != nil {
			return err
		}
		if err := resellerAccepting(&a); err != nil {
			return err
		}
		return database.GetDB().Transaction(func(tx *gorm.DB) error {
			var err error
			out, err = transferClientsTx(tx, ids, &a)
			return err
		})
	})
	return out, err
}

// ReleaseClients hands owned clients back to the administrator.
func (s *ResellerService) ReleaseClients(user *model.User, id int, clientIDs []int) (*ResellerTransferResult, error) {
	if !session.IsAdminUser(user) {
		return nil, ErrResellerForbidden
	}
	ids, err := uniqueClientIDs(clientIDs)
	if err != nil {
		return nil, err
	}
	var out *ResellerTransferResult
	err = s.WithAccountLock(id, func() error {
		return database.GetDB().Transaction(func(tx *gorm.DB) error {
			var owned int64
			if err := tx.Model(&model.ClientRecord{}).Where("id IN ? AND owner_user_id = ?", ids, id).Count(&owned).Error; err != nil {
				return err
			}
			if owned != int64(len(ids)) {
				return ErrResellerForbidden
			}
			var err error
			out, err = transferClientsTx(tx, ids, nil)
			return err
		})
	})
	return out, err
}

// transferClientsTx re-owns the rows; the database triggers keep the spent traffic with the old owner.
// A nil target is the administrator. Plan assignments the new owner cannot see are cleared.
func transferClientsTx(tx *gorm.DB, ids []int, target *model.ResellerAccount) (*ResellerTransferResult, error) {
	var recs []model.ClientRecord
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id IN ?", ids).Order("id").Find(&recs).Error; err != nil {
		return nil, err
	}
	if len(recs) != len(ids) {
		return nil, fmt.Errorf("%w: unknown client", ErrResellerInvalid)
	}
	out := &ResellerTransferResult{}
	var owner *int
	scope := PlanScope{}
	if target != nil {
		owner, scope.OwnerUserID = &target.UserID, &target.UserID
		if err := validateResellerIntake(tx, recs, target); err != nil {
			return nil, err
		}
	}
	for _, rec := range recs {
		if rec.OwnerUserID == nil && owner == nil || rec.OwnerUserID != nil && owner != nil && *rec.OwnerUserID == *owner {
			continue
		}
		out.Moved++
	}
	if err := tx.Model(&model.ClientRecord{}).Where("id IN ?", ids).Update("owner_user_id", owner).Error; err != nil {
		return nil, err
	}
	if err := tx.Where("client_id IN ?", ids).Delete(&model.ResellerPausedClient{}).Error; err != nil {
		return nil, err
	}
	var emails []string
	if err := tx.Table("client_policies cp").Joins("JOIN policies p ON p.id = cp.policy_id").Joins("JOIN clients c ON c.email = cp.email").
		Where("c.id IN ? AND p.owner_user_id IS DISTINCT FROM ?", ids, owner).Order("cp.email").Pluck("cp.email", &emails).Error; err != nil {
		return nil, err
	}
	for _, email := range emails {
		if err := (&PolicyService{}).UnassignPlanTx(tx, scope, email); err != nil {
			return nil, err
		}
		out.PlansCleared++
	}
	return out, nil
}

// validateResellerIntake answers with the panel's own errors before a trigger can answer with SQLSTATE.
func validateResellerIntake(tx *gorm.DB, recs []model.ClientRecord, target *model.ResellerAccount) error {
	incoming := 0
	ids := make([]int, 0, len(recs))
	for _, rec := range recs {
		ids = append(ids, rec.Id)
		if rec.Group != "" {
			return fmt.Errorf("%w: %s belongs to a global group", ErrResellerInvalid, rec.Email)
		}
		if target.ExpiresAt > 0 && (rec.ExpiryTime == 0 || rec.ExpiryTime > target.ExpiresAt) {
			return fmt.Errorf("%w: %s outlives the account term", ErrResellerBudget, rec.Email)
		}
		if target.TrafficLimit > 0 && rec.TotalGB == 0 {
			return fmt.Errorf("%w: %s has no quota under a finite budget", ErrResellerBudget, rec.Email)
		}
		if rec.OwnerUserID == nil || *rec.OwnerUserID != target.UserID {
			incoming++
		}
	}
	var ungranted []int
	if err := tx.Model(&model.ClientInbound{}).Where("client_id IN ? AND inbound_id NOT IN (?)", ids,
		tx.Model(&model.ResellerInboundGrant{}).Select("inbound_id").Where("user_id = ?", target.UserID)).Distinct().Pluck("inbound_id", &ungranted).Error; err != nil {
		return err
	}
	if len(ungranted) > 0 {
		return fmt.Errorf("%w: inbound %d is not granted to this reseller", ErrResellerForbidden, ungranted[0])
	}
	if target.ClientCap > 0 {
		var owned int64
		if err := tx.Model(&model.ClientRecord{}).Where("owner_user_id = ?", target.UserID).Count(&owned).Error; err != nil {
			return err
		}
		if owned+int64(incoming) > int64(target.ClientCap) {
			return fmt.Errorf("%w: client cap reached", ErrResellerBudget)
		}
	}
	return nil
}

// Delete removes the account and everything only it owned; clients are deleted or re-owned first.
// A runtime failure while deleting clients leaves the account in place for a retry.
func (s *ResellerService) Delete(user *model.User, id int, in ResellerDeleteRequest) error {
	if !session.IsAdminUser(user) {
		return ErrResellerForbidden
	}
	if in.Clients != "delete" && in.Clients != "transfer" || in.Clients == "transfer" && (in.OwnerID < 0 || in.OwnerID == id) {
		return ErrResellerInvalid
	}
	return s.withAccountLocks([]int{id, in.OwnerID}, func() error {
		db := database.GetDB()
		var a model.ResellerAccount
		if err := db.First(&a, "user_id = ?", id).Error; err != nil {
			return err
		}
		var owned []model.ClientRecord
		if err := db.Select("id", "email").Where("owner_user_id = ?", id).Order("id").Find(&owned).Error; err != nil {
			return err
		}
		ids := make([]int, 0, len(owned))
		emails := make([]string, 0, len(owned))
		for _, rec := range owned {
			ids = append(ids, rec.Id)
			emails = append(emails, rec.Email)
		}
		if in.Clients == "delete" && len(emails) > 0 {
			result, nr, err := (&ClientService{}).BulkDelete(&InboundService{}, emails, false)
			resellerApplyPending(nr, false)
			if err != nil {
				return err
			}
			if len(result.Skipped) > 0 {
				return fmt.Errorf("%d of %d clients could not be deleted (%s: %s); the account is kept for a retry",
					len(result.Skipped), len(emails), result.Skipped[0].Email, result.Skipped[0].Reason)
			}
		}
		return db.Transaction(func(tx *gorm.DB) error {
			if in.Clients == "transfer" && len(ids) > 0 {
				var target *model.ResellerAccount
				if in.OwnerID > 0 {
					target = &model.ResellerAccount{}
					if err := tx.First(target, "user_id = ?", in.OwnerID).Error; err != nil {
						if errors.Is(err, gorm.ErrRecordNotFound) {
							return fmt.Errorf("%w: unknown target reseller", ErrResellerInvalid)
						}
						return err
					}
					if err := resellerAccepting(target); err != nil {
						return err
					}
				}
				if _, err := transferClientsTx(tx, ids, target); err != nil {
					return err
				}
			}
			for _, step := range []*gorm.DB{
				tx.Where("owner_user_id = ?", id).Delete(&model.Policy{}),
				tx.Where("user_id = ?", id).Delete(&model.ResellerAllocation{}),
				tx.Where("audience_user_id = ?", id).Delete(&model.ActivityEvent{}),
				tx.Where("user_id = ?", id).Delete(&model.ActivityRead{}),
				tx.Where("user_id = ?", id).Delete(&model.ResellerAccount{}),
				tx.Where("id = ? AND role = ?", id, "reseller").Delete(&model.User{}),
			} {
				if step.Error != nil {
					return step.Error
				}
			}
			return nil
		})
	})
}
