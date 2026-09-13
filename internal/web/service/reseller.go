package service

import (
	"errors"
	"fmt"
	"math"
	"net/mail"
	"regexp"
	"slices"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/util/crypto"
	"github.com/Arman2122/p-ui/internal/web/session"
)

var (
	ErrResellerForbidden = errors.New("reseller access denied")
	ErrResellerBudget    = errors.New("reseller budget exceeded")
	ErrResellerInvalid   = errors.New("invalid reseller input")
	resellerHandle       = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{2,63}$`)
)

type ResellerService struct{}

type ResellerInput struct {
	Name              string `json:"name" example:"Sina M."`
	Username          string `json:"username" example:"sina"`
	Email             string `json:"email" example:"sina@example.com"`
	Password          string `json:"password" example:"use-a-unique-password"`
	TrafficLimit      int64  `json:"trafficLimit" example:"536870912000"`
	ClientCap         int    `json:"clientCap" example:"50"`
	ExpiresAt         int64  `json:"expiresAt" example:"1800000000000"`
	InboundIDs        []int  `json:"inboundIds" example:"[1,2]"`
	AllowPlans        bool   `json:"allowPlans" example:"true"`
	AllowReset        bool   `json:"allowReset" example:"true"`
	AllowExtend       bool   `json:"allowExtend" example:"false"`
	AllowSuspend      bool   `json:"allowSuspend" example:"true"`
	AllowTopUp        bool   `json:"allowTopUp" example:"true"`
	NotifyOnDepletion bool   `json:"notifyOnDepletion" example:"true"`
	OnDepletion       string `json:"onDepletion" example:"disable"`
	ResetCycle        string `json:"resetCycle" example:"none"`
	Note              string `json:"note" example:"Partner account"`
}

type ResellerView struct {
	Account     model.ResellerAccount `json:"account"`
	Username    string                `json:"username" example:"sina"`
	Clients     int64                 `json:"clients" example:"12"`
	TrafficUsed int64                 `json:"trafficUsed" example:"10737418240"`
	InboundIDs  []int                 `json:"inboundIds" example:"[1,2]"`
	NextResetAt int64                 `json:"nextResetAt" example:"1792000000000"`
}

type ResellerPermissions struct {
	Plans   bool `json:"plans" example:"true"`
	Reset   bool `json:"reset" example:"true"`
	Extend  bool `json:"extend" example:"false"`
	Suspend bool `json:"suspend" example:"true"`
	TopUp   bool `json:"topUp" example:"true"`
}

type SignedInUser struct {
	ID          int                 `json:"id" example:"2"`
	Username    string              `json:"username" example:"sina"`
	Role        string              `json:"role" example:"reseller"`
	Permissions ResellerPermissions `json:"permissions"`
	Home        string              `json:"home" example:"/reseller"`
}

func (s *ResellerService) CheckAccess(user *model.User) error {
	if user == nil || user.Role != "reseller" {
		return ErrResellerForbidden
	}
	var a model.ResellerAccount
	if err := database.GetDB().First(&a, "user_id = ?", user.Id).Error; err != nil {
		return ErrResellerForbidden
	}
	if a.Suspended || a.ExpiresAt > 0 && a.ExpiresAt <= time.Now().UnixMilli() {
		return ErrResellerForbidden
	}
	return nil
}

func (s *ResellerService) CanManagePlans(user *model.User) error {
	if err := s.CheckAccess(user); err != nil {
		return err
	}
	var a model.ResellerAccount
	if err := database.GetDB().First(&a, "user_id = ?", user.Id).Error; err != nil {
		return err
	}
	if !a.AllowPlans {
		return ErrResellerForbidden
	}
	return nil
}

func (s *ResellerService) Me(user *model.User) (*SignedInUser, error) {
	if user == nil {
		return nil, ErrResellerForbidden
	}
	out := &SignedInUser{ID: user.Id, Username: user.Username, Role: "admin", Home: "/"}
	if session.IsAdminUser(user) {
		return out, nil
	}
	if err := s.CheckAccess(user); err != nil {
		return nil, err
	}
	var a model.ResellerAccount
	if err := database.GetDB().First(&a, "user_id = ?", user.Id).Error; err != nil {
		return nil, err
	}
	out.Role, out.Home = "reseller", "/reseller"
	out.Permissions = ResellerPermissions{a.AllowPlans, a.AllowReset, a.AllowExtend, a.AllowSuspend, a.AllowTopUp}
	return out, nil
}

// The advisory transaction serializes reservations and runtime changes across panel processes.
// ClientService owns its transactions; the lock transaction never locks their rows.
func (s *ResellerService) WithAccountLock(userID int, fn func() error) error {
	return database.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec("SELECT pg_advisory_xact_lock(73421, ?)", userID).Error; err != nil {
			return err
		}
		return fn()
	})
}

func (s *ResellerService) Get(user *model.User, id int) (*ResellerView, error) {
	if !session.IsAdminUser(user) {
		if err := s.CheckAccess(user); err != nil {
			return nil, err
		}
		if user.Id != id {
			return nil, ErrResellerForbidden
		}
	}
	db := database.GetDB()
	var a model.ResellerAccount
	if err := db.First(&a, "user_id = ?", id).Error; err != nil {
		return nil, err
	}
	var u model.User
	if err := db.First(&u, id).Error; err != nil {
		return nil, err
	}
	out := &ResellerView{Account: a, Username: u.Username, InboundIDs: []int{}}
	if !session.IsAdminUser(user) {
		out.Account.Note = ""
	}
	if err := db.Model(&model.ClientRecord{}).Where("owner_user_id = ?", id).Count(&out.Clients).Error; err != nil {
		return nil, err
	}
	out.TrafficUsed = a.TrafficUsed
	if err := db.Model(&model.ResellerAllocation{}).Where("user_id = ?", id).
		Select("COALESCE(SUM(bytes),0)").Scan(&out.Account.TrafficAllocated).Error; err != nil {
		return nil, err
	}
	if err := db.Model(&model.ResellerInboundGrant{}).Where("user_id = ?", id).Order("inbound_id").Pluck("inbound_id", &out.InboundIDs).Error; err != nil {
		return nil, err
	}
	out.NextResetAt = resellerNextReset(&a)
	return out, nil
}

func (s *ResellerService) List(user *model.User) ([]*ResellerView, error) {
	if !session.IsAdminUser(user) {
		return nil, ErrResellerForbidden
	}
	var ids []int
	if err := database.GetDB().Model(&model.ResellerAccount{}).Order("user_id").Pluck("user_id", &ids).Error; err != nil {
		return nil, err
	}
	out := make([]*ResellerView, 0, len(ids))
	for _, id := range ids {
		v, err := s.Get(user, id)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

func validateResellerInput(in *ResellerInput, creating bool) error {
	in.Name, in.Username, in.Email = strings.TrimSpace(in.Name), strings.TrimSpace(in.Username), strings.TrimSpace(in.Email)
	if in.Name == "" || len(in.Name) > 128 || !resellerHandle.MatchString(in.Username) || len(in.Note) > 2048 || len(in.InboundIDs) == 0 || len(in.InboundIDs) > 1000 {
		return ErrResellerInvalid
	}
	if in.Email != "" {
		if _, err := mail.ParseAddress(in.Email); err != nil {
			return ErrResellerInvalid
		}
	}
	if creating && len(in.Password) < 12 || in.Password != "" && (len(in.Password) < 12 || len(in.Password) > 72) {
		return fmt.Errorf("%w: password must contain 12 to 72 bytes", ErrResellerInvalid)
	}
	if in.ClientCap < 0 || in.TrafficLimit < 0 || in.ExpiresAt < 0 || in.ExpiresAt > 0 && in.ExpiresAt <= time.Now().UnixMilli() {
		return ErrResellerInvalid
	}
	if in.OnDepletion == "" {
		in.OnDepletion = "disable"
	}
	if in.ResetCycle == "" {
		in.ResetCycle = "none"
	}
	if !slices.Contains([]string{"disable", "block"}, in.OnDepletion) || !slices.Contains([]string{"none", "weekly", "monthly"}, in.ResetCycle) {
		return ErrResellerInvalid
	}
	return nil
}

func (s *ResellerService) Save(user *model.User, id int, in ResellerInput) (*ResellerView, error) {
	if !session.IsAdminUser(user) {
		return nil, ErrResellerForbidden
	}
	if err := validateResellerInput(&in, id == 0); err != nil {
		return nil, err
	}
	var hash string
	if in.Password != "" {
		var err error
		hash, err = crypto.HashPasswordAsBcrypt(in.Password)
		if err != nil {
			return nil, err
		}
	}
	err := s.WithAccountLock(id, func() error {
		return database.GetDB().Transaction(func(tx *gorm.DB) error {
			var u model.User
			var a model.ResellerAccount
			if id > 0 {
				if err := tx.First(&a, "user_id = ?", id).Error; err != nil {
					return err
				}
				if err := tx.First(&u, "id = ? AND role = ?", id, "reseller").Error; err != nil {
					return err
				}
			} else {
				u.Role = "reseller"
			}
			var duplicates int64
			if err := tx.Model(&model.User{}).Where("lower(username) = lower(?) AND id <> ?", in.Username, id).Count(&duplicates).Error; err != nil {
				return err
			}
			if duplicates > 0 {
				return fmt.Errorf("%w: username is already in use", ErrResellerInvalid)
			}
			if in.TrafficLimit > 0 && a.TrafficUsed > in.TrafficLimit {
				return ErrResellerBudget
			}
			var clients int64
			if id > 0 {
				if err := tx.Model(&model.ClientRecord{}).Where("owner_user_id = ?", id).Count(&clients).Error; err != nil {
					return err
				}
				if in.ClientCap > 0 && clients > int64(in.ClientCap) {
					return ErrResellerBudget
				}
				var exceeding int64
				if in.ExpiresAt > 0 {
					if err := tx.Model(&model.ClientRecord{}).Where("owner_user_id = ? AND (expiry_time <= 0 OR expiry_time > ?)", id, in.ExpiresAt).Count(&exceeding).Error; err != nil {
						return err
					}
				}
				if exceeding > 0 {
					return fmt.Errorf("%w: existing clients outlive the proposed term", ErrResellerBudget)
				}
				if err := tx.Table("client_inbounds ci").Joins("JOIN clients c ON c.id = ci.client_id").Where("c.owner_user_id = ? AND ci.inbound_id NOT IN ?", id, in.InboundIDs).Count(&exceeding).Error; err != nil {
					return err
				}
				if exceeding > 0 {
					return fmt.Errorf("%w: an inbound still has owned clients", ErrResellerInvalid)
				}
			}
			if err := validateResellerGrants(tx, 0, in.InboundIDs); err != nil {
				return err
			}
			u.Username = in.Username
			if hash != "" {
				u.Password = hash
				u.LoginEpoch++
			}
			if err := tx.Save(&u).Error; err != nil {
				return err
			}
			id = u.Id
			a.UserID, a.Name, a.Email, a.TrafficLimit, a.ClientCap, a.ExpiresAt = id, in.Name, in.Email, in.TrafficLimit, in.ClientCap, in.ExpiresAt
			a.AllowPlans, a.AllowReset, a.AllowExtend, a.AllowSuspend, a.AllowTopUp = in.AllowPlans, in.AllowReset, in.AllowExtend, in.AllowSuspend, in.AllowTopUp
			a.NotifyOnDepletion, a.Note, a.OnDepletion = in.NotifyOnDepletion, in.Note, in.OnDepletion
			// Switching a cycle on starts the period now; nothing already spent is forgiven.
			if in.ResetCycle != a.ResetCycle && in.ResetCycle != "none" {
				a.PeriodStartedAt = time.Now().UnixMilli()
			}
			a.ResetCycle = in.ResetCycle
			if err := tx.Omit("TrafficUsed", "BudgetBlocked", "TrafficWarnedAt", "ExpiryWarnedAt").Save(&a).Error; err != nil {
				return err
			}
			if err := tx.Where("user_id = ? AND inbound_id NOT IN ?", id, in.InboundIDs).Delete(&model.ResellerInboundGrant{}).Error; err != nil {
				return err
			}
			for _, ib := range in.InboundIDs {
				if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&model.ResellerInboundGrant{UserID: id, InboundID: ib}).Error; err != nil {
					return err
				}
			}
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return s.Get(user, id)
}

func validateResellerGrants(db *gorm.DB, userID int, ids []int) error {
	seen := map[int]bool{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return ErrResellerInvalid
		}
		seen[id] = true
		var n int64
		q := db.Model(&model.Inbound{}).Where("id = ?", id)
		if userID > 0 {
			q = q.Where("enable = ?", true).Where("id IN (?)", db.Model(&model.ResellerInboundGrant{}).Select("inbound_id").Where("user_id = ?", userID))
		}
		if err := q.Count(&n).Error; err != nil {
			return err
		}
		if n != 1 {
			return ErrResellerForbidden
		}
	}
	return nil
}

func (s *ResellerService) TopUp(user *model.User, id int, traffic int64, days int, clients int) error {
	if !session.IsAdminUser(user) {
		return ErrResellerForbidden
	}
	if traffic < 0 || days < 0 || days > 36500 || clients < 0 || traffic == 0 && days == 0 && clients == 0 {
		return ErrResellerInvalid
	}
	return s.WithAccountLock(id, func() error {
		var a model.ResellerAccount
		if err := database.GetDB().First(&a, "user_id = ?", id).Error; err != nil {
			return err
		}
		if a.TrafficLimit == 0 && traffic > 0 || a.ClientCap == 0 && clients > 0 || a.ExpiresAt == 0 && days > 0 {
			return fmt.Errorf("%w: an unlimited axis cannot be topped up", ErrResellerInvalid)
		}
		if traffic > math.MaxInt64-a.TrafficLimit || clients > math.MaxInt-a.ClientCap {
			return ErrResellerInvalid
		}
		a.TrafficLimit += traffic
		a.ClientCap += clients
		if days > 0 {
			a.ExpiresAt = max(a.ExpiresAt, time.Now().UnixMilli()) + int64(days)*86400000
		}
		if err := database.GetDB().Model(&a).Updates(map[string]any{"traffic_limit": a.TrafficLimit, "client_cap": a.ClientCap, "expires_at": a.ExpiresAt}).Error; err != nil {
			return err
		}
		_, err := s.convergeBudgetLocked(id, time.Now())
		return err
	})
}
