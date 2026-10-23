package panel

import (
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/util/crypto"
	ldaputil "github.com/Arman2122/p-ui/internal/util/ldap"
	"github.com/Arman2122/p-ui/internal/util/totp"
	"github.com/Arman2122/p-ui/internal/web/service"
	"github.com/Arman2122/p-ui/internal/web/session"
)

// UserService provides business logic for user management and authentication.
// It handles user creation, login, password management, and 2FA operations.
type UserService struct {
	settingService service.SettingService
}

// GetFirstUser retrieves the first user from the database.
// This is typically used for initial setup or when there's only one admin user.
func (s *UserService) GetFirstUser() (*model.User, error) {
	db := database.GetDB()

	user := &model.User{}
	err := db.Model(model.User{}).
		Where("role = ? OR role = ?", "admin", "").
		First(user).
		Error
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (s *UserService) CheckUser(username string, password string, twoFactorCode string) (*model.User, error) {
	db := database.GetDB()

	user := &model.User{}

	err := db.Model(model.User{}).
		Where("username = ?", username).
		First(user).
		Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// The same bcrypt a real check costs, so a failed login takes as long
		// for a username that does not exist as for one that does. F212.
		_ = crypto.CheckPasswordHash(equalCostHash, password)
		return nil, errors.New("invalid credentials")
	} else if err != nil {
		logger.Warning("check user err:", err)
		return nil, err
	}

	if !crypto.CheckPasswordHash(user.Password, password) {
		if !session.IsAdminUser(user) {
			return nil, errors.New("invalid credentials")
		}
		ldapEnabled, _ := s.settingService.GetLdapEnable()
		if !ldapEnabled {
			return nil, errors.New("invalid credentials")
		}

		/*
			Panel login through LDAP is its own opt-in, and an empty filter is off.

			ldapEnable is the CLIENT-SYNC switch. Letting it also authorise
			administrators meant every account the directory returns for
			ldapUserFilter — which defaults to (objectClass=person), i.e. everyone —
			could sign in here as the panel admin with their own password, because
			AuthenticateUser proves only that the person exists and knows their own
			credentials. It proves nothing about administering this panel.

			Nobody is locked out by this: LDAP is only reached AFTER the local
			password fails, so the panel account always works.
		*/
		adminFilter, _ := s.settingService.GetLdapAdminFilter()
		if strings.TrimSpace(adminFilter) == "" {
			return nil, errors.New("invalid credentials")
		}
		host, _ := s.settingService.GetLdapHost()
		port, _ := s.settingService.GetLdapPort()
		useTLS, _ := s.settingService.GetLdapUseTLS()
		skipVerify, _ := s.settingService.GetLdapInsecureSkipVerify()
		bindDN, _ := s.settingService.GetLdapBindDN()
		ldapPass, _ := s.settingService.GetLdapPassword()
		baseDN, _ := s.settingService.GetLdapBaseDN()
		userFilter, _ := s.settingService.GetLdapUserFilter()
		userAttr, _ := s.settingService.GetLdapUserAttr()

		cfg := ldaputil.Config{
			Host:               host,
			Port:               port,
			UseTLS:             useTLS,
			InsecureSkipVerify: skipVerify,
			BindDN:             bindDN,
			Password:           ldapPass,
			BaseDN:             baseDN,
			UserFilter:         userFilter,
			UserAttr:           userAttr,
			AdminFilter:        strings.TrimSpace(adminFilter),
		}
		ok, err := ldaputil.AuthenticateUser(cfg, username, password)
		if err != nil || !ok {
			return nil, errors.New("invalid credentials")
		}
	}

	twoFactorEnable, err := s.settingService.GetTwoFactorEnable()
	if err != nil {
		logger.Warning("check two factor err:", err)
		return nil, err
	}

	if twoFactorEnable && session.IsAdminUser(user) {
		twoFactorToken, err := s.settingService.GetTwoFactorToken()
		if err != nil {
			logger.Warning("check two factor token err:", err)
			return nil, err
		}

		// Empty-key TOTP codes are publicly computable and never authenticate anyone.
		if strings.TrimSpace(twoFactorToken) == "" {
			return nil, errors.New("invalid 2fa code")
		}

		if !totp.VerifyWithSkew(twoFactorToken, twoFactorCode, time.Now()) {
			return nil, errors.New("invalid 2fa code")
		}
	}

	if !session.IsAdminUser(user) {
		if err := (&service.ResellerService{}).CheckAccess(user); err != nil {
			return nil, errors.New("invalid credentials")
		}
	}
	return user, nil
}

func (s *UserService) BumpLoginEpoch(userIDs ...int) error {
	db := database.GetDB()
	if db == nil {
		// logout's contract is that a failed revocation is logged and the browser
		// is still cleared. A nil dereference here would defeat exactly that.
		return errors.New("panel: no database connection to revoke sessions with")
	}
	q := db.Model(model.User{}).Where("role = ? OR role = ?", "admin", "")
	if len(userIDs) > 0 {
		q = db.Model(model.User{}).Where("id = ?", userIDs[0])
	}
	return q.
		Update("login_epoch", gorm.Expr("login_epoch + 1")).
		Error
}

func (s *UserService) UpdateUser(id int, username string, password string) error {
	db := database.GetDB()
	var target model.User
	if err := db.First(&target, id).Error; err != nil {
		return err
	}
	if !session.IsAdminUser(&target) {
		return service.ErrResellerForbidden
	}
	hashedPassword, err := crypto.HashPasswordAsBcrypt(password)
	if err != nil {
		return err
	}

	twoFactorEnable, err := s.settingService.GetTwoFactorEnable()
	if err != nil {
		return err
	}

	if twoFactorEnable {
		_ = s.settingService.SetTwoFactorEnable(false)
		_ = s.settingService.SetTwoFactorToken("")
	}

	return db.Model(model.User{}).
		Where("id = ?", id).
		Updates(map[string]any{
			"username":    username,
			"password":    hashedPassword,
			"login_epoch": gorm.Expr("login_epoch + 1"),
		}).
		Error
}

func (s *UserService) UpdateFirstUser(username string, password string) error {
	if username == "" {
		return errors.New("username can not be empty")
	} else if password == "" {
		return errors.New("password can not be empty")
	}
	hashedPassword, er := crypto.HashPasswordAsBcrypt(password)

	if er != nil {
		return er
	}

	db := database.GetDB()
	user := &model.User{}
	err := db.Model(model.User{}).Where("role = ? OR role = ?", "admin", "").First(user).Error
	if database.IsNotFound(err) {
		user.Username = username
		user.Password = hashedPassword
		return db.Model(model.User{}).Create(user).Error
	} else if err != nil {
		return err
	}
	user.Username = username
	user.Password = hashedPassword
	user.LoginEpoch++
	return db.Save(user).Error
}

// equalCostHash is a bcrypt hash at the panel's own cost, verified against on
// the not-found branch so an unknown username is not the fast answer. F212.
var equalCostHash = mustEqualCostHash()

func mustEqualCostHash() string {
	hash, err := crypto.HashPasswordAsBcrypt("p-ui-equal-cost")
	if err != nil {
		panic("bcrypt at the default cost failed: " + err.Error())
	}
	return hash
}
