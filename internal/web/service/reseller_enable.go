package service

import (
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func ValidateClientOwnerEnable(email string) error {
	var rec model.ClientRecord
	if err := database.GetDB().Select("owner_user_id").Where("email = ?", email).First(&rec).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if rec.OwnerUserID == nil {
		return nil
	}
	var a model.ResellerAccount
	if err := database.GetDB().First(&a, "user_id = ?", *rec.OwnerUserID).Error; err != nil {
		return ErrResellerForbidden
	}
	if a.Suspended || a.BudgetBlocked || a.ExpiresAt > 0 && a.ExpiresAt <= time.Now().UnixMilli() || a.TrafficLimit > 0 && a.TrafficUsed >= a.TrafficLimit {
		return ErrResellerBudget
	}
	return nil
}

func ValidateClientOwnerInbounds(email string, inboundIDs []int) error {
	var rec model.ClientRecord
	db := database.GetDB()
	if err := db.Select("owner_user_id").Where("email = ?", email).First(&rec).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	if rec.OwnerUserID == nil {
		return nil
	}
	seen := map[int]bool{}
	for _, id := range inboundIDs {
		if id <= 0 {
			return ErrResellerInvalid
		}
		seen[id] = true
	}
	ids := make([]int, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	var n int64
	if err := db.Model(&model.ResellerInboundGrant{}).Where("user_id = ? AND inbound_id IN ?", *rec.OwnerUserID, ids).Count(&n).Error; err != nil {
		return err
	}
	if n != int64(len(ids)) {
		return ErrResellerForbidden
	}
	return nil
}

func ValidateClientOwnerGroup(email, group string) error {
	if group == "" {
		return nil
	}
	var n int64
	if err := database.GetDB().Model(&model.ClientRecord{}).Where("email = ? AND owner_user_id IS NOT NULL", email).Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return ErrResellerForbidden
	}
	return nil
}
