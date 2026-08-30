// Package locale provides internationalization (i18n) support for the Penhoon UI web panel,
// including translation loading, localization, and middleware for web and bot interfaces.
package locale

import (
	"embed"
	"encoding/json"
	"io/fs"
	"os"
	"strings"

	"github.com/Arman2122/p-ui/internal/logger"

	"github.com/gin-gonic/gin"
	"github.com/nicksnyder/go-i18n/v2/i18n"
	"golang.org/x/text/language"
)

var (
	i18nBundle *i18n.Bundle

	/*
		The bot has one language for the whole panel, so one localizer is right.

		The web does NOT, and used to have a package-level LocalizerWeb that
		LocalizerMiddleware overwrote on every request — with no lock, from two
		HTTP servers in one process. So a subscriber's Accept-Language decided the
		language an admin's concurrent response rendered in, and the write raced
		every read. The web localizer lives on the request now. F407.
	*/
	LocalizerBot *i18n.Localizer
)

// I18nType represents the type of interface for internationalization.
type I18nType string

const (
	Bot I18nType = "bot" // Bot interface type
	Web I18nType = "web" // Web interface type
)

// SettingService interface defines methods for accessing locale settings.
type SettingService interface {
	GetTgLang() (string, error)
}

// InitLocalizer initializes the internationalization system with embedded translation files.
func InitLocalizer(i18nFS embed.FS, settingService SettingService) error {
	// set default bundle to English
	i18nBundle = i18n.NewBundle(language.MustParse("en-US"))
	i18nBundle.RegisterUnmarshalFunc("json", json.Unmarshal)

	// parse files
	if err := parseTranslationFiles(i18nFS, i18nBundle); err != nil {
		return err
	}

	// setup bot locale
	if err := initTGBotLocalizer(settingService); err != nil {
		return err
	}

	return nil
}

// createTemplateData creates a template data map from parameters with optional separator.
func createTemplateData(params []string, separator ...string) map[string]any {
	sep := "=="
	if len(separator) > 0 {
		sep = separator[0]
	}

	templateData := make(map[string]any)
	for _, param := range params {
		parts := strings.SplitN(param, sep, 2)
		templateData[parts[0]] = parts[1]
	}

	return templateData
}

// I18n retrieves a localized message for the given key and type.
// It supports both bot and web contexts, with optional template parameters.
// Returns the localized message or an empty string if localization fails.
func I18n(i18nType I18nType, key string, params ...string) string {
	switch i18nType {
	case Bot:
		return localize(LocalizerBot, key, params...)
	case Web:
		// A web string's language comes from the request asking for it, and there
		// is no request here. LocalizerMiddleware puts a function bound to the
		// right localizer into the context and I18nWeb calls that; reaching this
		// arm means somebody asked for a web string outside a request, where the
		// key is the honest answer.
		return key
	default:
		logger.Errorf("Invalid type for I18n: %s", i18nType)
		return ""
	}
}

// localize renders one message, or the key when there is no localizer to render
// it with — a missing localizer must never panic a page.
func localize(localizer *i18n.Localizer, key string, params ...string) string {
	if localizer == nil {
		return key
	}
	msg, err := localizer.Localize(&i18n.LocalizeConfig{
		MessageID:    key,
		TemplateData: createTemplateData(params),
	})
	if err != nil {
		logger.Errorf("Failed to localize message: %v", err)
		return ""
	}
	return msg
}

// initTGBotLocalizer initializes the bot localizer with the configured language.
func initTGBotLocalizer(settingService SettingService) error {
	botLang, err := settingService.GetTgLang()
	if err != nil {
		return err
	}

	LocalizerBot = i18n.NewLocalizer(i18nBundle, botLang)
	return nil
}

// LocalizerMiddleware returns a Gin middleware that sets up localization for web requests.
// It determines the user's language from cookies or Accept-Language header,
// creates a localizer instance, and stores it in the Gin context for use in handlers.
// Also provides the I18n function in the context for template rendering.
func LocalizerMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Ensure bundle is initialized so creating a Localizer won't panic
		if i18nBundle == nil {
			i18nBundle = i18n.NewBundle(language.MustParse("en-US"))
			i18nBundle.RegisterUnmarshalFunc("json", json.Unmarshal)
			// Try lazy-load from disk when running sub server without InitLocalizer
			if err := loadTranslationsFromDisk(i18nBundle); err != nil {
				logger.Warning("i18n lazy load failed:", err)
			}
		}
		var lang string

		if cookie, err := c.Request.Cookie("lang"); err == nil {
			lang = cookie.Value
		} else {
			lang = c.GetHeader("Accept-Language")
		}

		// This request's own localizer, and a function bound to it. Nothing
		// shared, so nothing to race and no language to bleed. F407.
		localizer := i18n.NewLocalizer(i18nBundle, lang)
		c.Set("localizer", localizer)
		c.Set("I18n", func(i18nType I18nType, key string, params ...string) string {
			if i18nType == Web {
				return localize(localizer, key, params...)
			}
			return I18n(i18nType, key, params...)
		})
		c.Next()
	}
}

// loadTranslationsFromDisk attempts to load translation files from "internal/web/translation" using the local filesystem.
func loadTranslationsFromDisk(bundle *i18n.Bundle) error {
	root := os.DirFS("internal/web")
	return fs.WalkDir(root, "translation", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(root, path)
		if err != nil {
			return err
		}
		_, err = bundle.ParseMessageFileBytes(data, path)
		return err
	})
}

// parseTranslationFiles parses embedded translation files and adds them to the i18n bundle.
func parseTranslationFiles(i18nFS embed.FS, i18nBundle *i18n.Bundle) error {
	err := fs.WalkDir(i18nFS, "translation",
		func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if d.IsDir() {
				return nil
			}

			data, err := i18nFS.ReadFile(path)
			if err != nil {
				return err
			}

			_, err = i18nBundle.ParseMessageFileBytes(data, path)
			return err
		})
	if err != nil {
		return err
	}

	return nil
}
