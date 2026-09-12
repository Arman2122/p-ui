package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/session"
)

func TestConfigAndBackupsRequireAdministratorWriteScope(t *testing.T) {
	for _, tc := range []struct{ name, role, scope string }{
		{"read token", "admin", "read"}, {"reseller", "reseller", "admin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			engine := gin.New()
			api := engine.Group("/panel/api", func(c *gin.Context) {
				session.SetAPIAuthUser(c, &model.User{Id: 1, Role: tc.role})
				c.Set("api_scope", tc.scope)
			})
			NewBackupController(api)
			NewCoreConfigController(api)
			for _, path := range []string{"/panel/api/backups", "/panel/api/backups/20260912T030000.000000000/download", "/panel/api/core-config/xray"} {
				recorder := httptest.NewRecorder()
				engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
				if recorder.Code != http.StatusForbidden {
					t.Fatalf("GET %s = %d, want 403", path, recorder.Code)
				}
			}
			recorder := httptest.NewRecorder()
			engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/panel/api/backups/20260912T030000.000000000/telegram", nil))
			if recorder.Code != http.StatusForbidden {
				t.Fatalf("Telegram transfer = %d, want 403", recorder.Code)
			}
		})
	}
}
