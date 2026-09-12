package service

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/util/crypto"
	"github.com/Arman2122/p-ui/internal/util/random"
)

type ResellerTokenView struct {
	ID        int    `json:"id" example:"1"`
	Name      string `json:"name" example:"Automation"`
	Token     string `json:"token,omitempty" example:"shown-only-at-creation"`
	ExpiresAt int64  `json:"expiresAt" example:"1800000000000"`
	CreatedAt int64  `json:"createdAt" example:"1790000000000"`
}

func (s *ResellerService) MatchToken(token string) (*model.User, error) {
	if token == "" {
		return nil, ErrResellerForbidden
	}
	var row model.ResellerToken
	if err := database.GetDB().Where("hash = ? AND (expires_at = 0 OR expires_at > ?)", crypto.HashTokenSHA256(token), time.Now().UnixMilli()).First(&row).Error; err != nil {
		return nil, ErrResellerForbidden
	}
	var user model.User
	if err := database.GetDB().First(&user, "id = ? AND role = ? AND login_epoch = ?", row.UserID, "reseller", row.LoginEpoch).Error; err != nil {
		return nil, ErrResellerForbidden
	}
	if err := s.CheckAccess(&user); err != nil {
		return nil, err
	}
	return &user, nil
}

func (s *ResellerService) Tokens(user *model.User) ([]ResellerTokenView, error) {
	if err := s.CheckAccess(user); err != nil {
		return nil, err
	}
	out := []ResellerTokenView{}
	err := database.GetDB().Model(&model.ResellerToken{}).Where("user_id = ?", user.Id).Select("id,name,expires_at,created_at").Order("id").Scan(&out).Error
	return out, err
}

func (s *ResellerService) CreateToken(user *model.User, name string, expires int64) (*ResellerTokenView, error) {
	if err := s.CheckAccess(user); err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 64 || expires <= time.Now().UnixMilli() {
		return nil, ErrResellerInvalid
	}
	var out *ResellerTokenView
	err := s.WithAccountLock(user.Id, func() error {
		if err := s.CheckAccess(user); err != nil {
			return err
		}
		var count int64
		if err := database.GetDB().Model(&model.ResellerToken{}).Where("user_id = ?", user.Id).Count(&count).Error; err != nil {
			return err
		}
		if count >= 20 {
			return ErrResellerBudget
		}
		token := random.Seq(48)
		row := model.ResellerToken{UserID: user.Id, Name: name, Hash: crypto.HashTokenSHA256(token), LoginEpoch: user.LoginEpoch, ExpiresAt: expires}
		if err := database.GetDB().Create(&row).Error; err != nil {
			return err
		}
		out = &ResellerTokenView{ID: row.ID, Name: row.Name, Token: token, ExpiresAt: expires, CreatedAt: row.CreatedAt}
		return nil
	})
	return out, err
}

func (s *ResellerService) DeleteToken(user *model.User, id int) error {
	if err := s.CheckAccess(user); err != nil {
		return err
	}
	res := database.GetDB().Where("id = ? AND user_id = ?", id, user.Id).Delete(&model.ResellerToken{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return ErrResellerForbidden
	}
	return nil
}

func (s *ResellerService) ChangePassword(user *model.User, current, password string) error {
	if err := s.CheckAccess(user); err != nil {
		return err
	}
	if len(password) < 12 || len(password) > 72 {
		return ErrResellerInvalid
	}
	return s.WithAccountLock(user.Id, func() error {
		var latest model.User
		if err := database.GetDB().First(&latest, "id = ? AND role = ?", user.Id, "reseller").Error; err != nil {
			return err
		}
		if !crypto.CheckPasswordHash(latest.Password, current) {
			return ErrResellerForbidden
		}
		hash, err := crypto.HashPasswordAsBcrypt(password)
		if err != nil {
			return err
		}
		return database.GetDB().Model(&latest).Updates(map[string]any{"password": hash, "login_epoch": gorm.Expr("login_epoch + 1")}).Error
	})
}

func LegacyTokenUser(token, sourceIP string) (*model.User, string, error) {
	var row model.ApiToken
	err := database.GetDB().Where("token = ? AND enabled = ? AND (expires_at = 0 OR expires_at > ?)", crypto.HashTokenSHA256(token), true, time.Now().Unix()).First(&row).Error
	if err != nil {
		return nil, "", ErrResellerForbidden
	}
	if row.Scope != "" && row.Scope != model.ApiTokenScopeAdmin && row.Scope != model.ApiTokenScopeRead {
		return nil, "", ErrResellerForbidden
	}
	if err := CheckAPITokenSource(token, sourceIP); err != nil {
		return nil, "", ErrResellerForbidden
	}
	var user model.User
	if err := database.GetDB().Where("role = ? OR role = ?", "admin", "").First(&user).Error; err != nil {
		return nil, "", errors.New("no administrator owns this legacy token")
	}
	scope := row.Scope
	if scope == "" {
		scope = model.ApiTokenScopeAdmin
	}
	return &user, scope, nil
}
