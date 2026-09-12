package panel

import (
	"errors"
	"strconv"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/service"
)

type SettingsAPITokenView struct {
	ApiTokenView
	AllowedIPs string `json:"allowedIPs" example:"192.0.2.10,2001:db8::/32"`
}

func tokenIPKey(id int) string { return "apiTokenAllowedIPs:" + strconv.Itoa(id) }

func (s *ApiTokenService) ListRestricted() ([]SettingsAPITokenView, error) {
	rows, err := s.List()
	if err != nil {
		return nil, err
	}
	views := make([]SettingsAPITokenView, 0, len(rows))
	for _, row := range rows {
		view := SettingsAPITokenView{ApiTokenView: *row}
		setting, err := tokenRestriction(row.Id)
		if err != nil && !database.IsNotFound(err) {
			return nil, err
		}
		if setting != nil {
			view.AllowedIPs = setting.Value
		}
		views = append(views, view)
	}
	return views, nil
}

func (s *ApiTokenService) CreateRestricted(name, scope string, expiresAt int64, allowedIPs string) (*SettingsAPITokenView, error) {
	allowedIPs, err := service.NormalizeTokenSourceNetworks(allowedIPs)
	if err != nil {
		return nil, err
	}
	tokens := s
	row, err := tokens.Create(name, scope, expiresAt)
	if err != nil {
		return nil, err
	}
	if err := database.GetDB().Create(&model.Setting{Key: tokenIPKey(row.Id), Value: allowedIPs}).Error; err != nil {
		return nil, errors.Join(err, tokens.Delete(row.Id))
	}
	return &SettingsAPITokenView{ApiTokenView: *row, AllowedIPs: allowedIPs}, nil
}

func (s *ApiTokenService) DeleteRestricted(id int) error {
	if id <= 0 {
		return errors.New("invalid token id")
	}
	return database.GetDB().Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ?", id).Delete(&model.ApiToken{}).Error; err != nil {
			return err
		}
		return tx.Where("key = ?", tokenIPKey(id)).Delete(&model.Setting{}).Error
	})
}

func tokenRestriction(id int) (*model.Setting, error) {
	var setting model.Setting
	err := database.GetDB().Where("key = ?", tokenIPKey(id)).First(&setting).Error
	return &setting, err
}
