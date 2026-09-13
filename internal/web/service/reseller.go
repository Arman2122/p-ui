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
	Online      int64                 `json:"online" example:"3"`
	Active      int64                 `json:"active" example:"10"`
	History     []ClientHistoryPoint  `json:"history"`
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
	views, err := s.views(database.GetDB().Where("user_id = ?", id), time.Now())
	if err != nil {
		return nil, err
	}
	if len(views) == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	if !session.IsAdminUser(user) {
		views[0].Account.Note = ""
	}
	return views[0], nil
}

func (s *ResellerService) List(user *model.User) ([]*ResellerView, error) {
	if !session.IsAdminUser(user) {
		return nil, ErrResellerForbidden
	}
	return s.views(database.GetDB(), time.Now())
}

// views reads every account in scope with the same six queries whatever the count.
func (s *ResellerService) views(scope *gorm.DB, now time.Time) ([]*ResellerView, error) {
	db := database.GetDB()
	var accounts []model.ResellerAccount
	if err := scope.Order("user_id").Find(&accounts).Error; err != nil {
		return nil, err
	}
	out := make([]*ResellerView, 0, len(accounts))
	if len(accounts) == 0 {
		return out, nil
	}
	byID := make(map[int]*ResellerView, len(accounts))
	ids := make([]int, 0, len(accounts))
	for _, a := range accounts {
		v := &ResellerView{Account: a, TrafficUsed: a.TrafficUsed, InboundIDs: []int{}, History: []ClientHistoryPoint{}, NextResetAt: resellerNextReset(&a)}
		v.Account.TrafficAllocated = 0
		out = append(out, v)
		byID[a.UserID] = v
		ids = append(ids, a.UserID)
	}
	var users []model.User
	if err := db.Select("id", "username").Where("id IN ?", ids).Find(&users).Error; err != nil {
		return nil, err
	}
	for _, u := range users {
		byID[u.Id].Username = u.Username
	}
	var allocations []struct {
		UserID int
		Bytes  int64
	}
	if err := db.Model(&model.ResellerAllocation{}).Select("user_id, COALESCE(SUM(bytes),0) AS bytes").Where("user_id IN ?", ids).Group("user_id").Scan(&allocations).Error; err != nil {
		return nil, err
	}
	for _, row := range allocations {
		byID[row.UserID].Account.TrafficAllocated = row.Bytes
	}
	var grants []model.ResellerInboundGrant
	if err := db.Where("user_id IN ?", ids).Order("inbound_id").Find(&grants).Error; err != nil {
		return nil, err
	}
	for _, g := range grants {
		byID[g.UserID].InboundIDs = append(byID[g.UserID].InboundIDs, g.InboundID)
	}
	used, usedArgs := UsedBytesExpr(db)
	ms := now.UnixMilli()
	var counts []struct {
		UserID                  int
		Clients, Online, Active int64
	}
	args := append([]any{ms - 30000, ms}, usedArgs...)
	if err := db.Table("clients c").Joins("LEFT JOIN client_traffics ON client_traffics.email = c.email").
		Select("c.owner_user_id AS user_id, COUNT(*) AS clients, COUNT(*) FILTER (WHERE client_traffics.last_online > ?) AS online, COUNT(*) FILTER (WHERE c.enable AND (c.expiry_time = 0 OR c.expiry_time > ?) AND (c.total_gb = 0 OR COALESCE("+used+",0) < c.total_gb)) AS active", args...).
		Where("c.owner_user_id IN ?", ids).Group("c.owner_user_id").Scan(&counts).Error; err != nil {
		return nil, err
	}
	for _, row := range counts {
		v := byID[row.UserID]
		v.Clients, v.Online, v.Active = row.Clients, row.Online, row.Active
	}
	var samples []struct {
		UserID             int
		ClientHistoryPoint `gorm:"embedded"`
	}
	since := now.Unix()/86400*86400 - 13*86400
	if err := db.Table("client_traffic_samples s").Joins("JOIN clients c ON c.email = s.email").
		Select("c.owner_user_id AS user_id, s.t / 86400 * 86400 AS t, SUM(s.up) AS up, SUM(s.down) AS down").
		Where("s.t >= ? AND c.owner_user_id IN ?", since, ids).Group("c.owner_user_id, s.t / 86400").Order("t").Scan(&samples).Error; err != nil {
		return nil, err
	}
	for _, row := range samples {
		byID[row.UserID].History = append(byID[row.UserID].History, row.ClientHistoryPoint)
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
