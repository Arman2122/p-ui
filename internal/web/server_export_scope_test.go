package web

import (
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/robfig/cron/v3"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/util/crypto"
	"github.com/Arman2122/p-ui/internal/web/global"
	"github.com/Arman2122/p-ui/internal/web/session"
)

func TestLegacyServerExportsRequireAdminScope(t *testing.T) {
	initTestDB(t)
	previous := global.GetWebServer()
	s := NewServer()
	s.cron = cron.New(cron.WithSeconds())
	global.SetWebServer(s)
	t.Cleanup(func() { s.cancel(); global.SetWebServer(previous) })
	engine, err := s.initRouter()
	if err != nil {
		t.Fatal(err)
	}
	var user model.User
	if err := database.GetDB().First(&user).Error; err != nil {
		t.Fatal(err)
	}
	engine.GET("/__export_scope_session", func(c *gin.Context) {
		if err := session.SetLoginUser(c, &user); err != nil {
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusOK)
	})
	login := httptest.NewRecorder()
	engine.ServeHTTP(login, httptest.NewRequest(http.MethodGet, "/__export_scope_session", nil))
	if login.Code != http.StatusOK || len(login.Result().Cookies()) == 0 {
		t.Fatalf("session setup status=%d", login.Code)
	}
	tokens := map[string]string{}
	for _, scope := range []string{model.ApiTokenScopeRead, model.ApiTokenScopeAdmin} {
		token := "server-export-scope-" + scope
		if err := database.GetDB().Create(&model.ApiToken{Name: token, Token: crypto.HashTokenSHA256(token), Scope: scope, Enabled: true}).Error; err != nil {
			t.Fatal(err)
		}
		tokens[scope] = token
	}
	// No real dump can run; authorized requests must reach the missing-exporter
	// error, while read tokens must be stopped earlier with HTTP 403.
	t.Setenv("PATH", t.TempDir())
	if _, err := exec.LookPath("pg_dump"); err == nil {
		t.Fatal("pg_dump unexpectedly available in isolated PATH")
	}
	for _, scope := range []string{model.ApiTokenScopeRead, model.ApiTokenScopeAdmin, "cookie"} {
		for _, path := range []string{"/panel/api/server/getDb", "/panel/api/server/getConfigJson", "/panel/api/server/status"} {
			t.Run(scope+path, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodGet, path, nil)
				if scope == "cookie" {
					for _, cookie := range login.Result().Cookies() {
						req.AddCookie(cookie)
					}
				} else {
					req.Header.Set("Authorization", "Bearer "+tokens[scope])
				}
				req.Header.Set("X-Requested-With", "XMLHttpRequest")
				response := httptest.NewRecorder()
				engine.ServeHTTP(response, req)
				if scope == model.ApiTokenScopeRead && path != "/panel/api/server/status" {
					if response.Code != http.StatusForbidden || strings.Contains(response.Body.String(), "pg_dump") {
						t.Fatalf("read export reached service: status=%d body=%s", response.Code, response.Body.String())
					}
					return
				}
				if response.Code != http.StatusOK {
					t.Fatalf("authorized request status=%d body=%s", response.Code, response.Body.String())
				}
				if path == "/panel/api/server/getDb" && !strings.Contains(response.Body.String(), "pg_dump not found") {
					t.Fatalf("admin did not reach exporter check: %s", response.Body.String())
				}
				if path != "/panel/api/server/status" && response.Header().Get("Cache-Control") != "no-store" {
					t.Fatal("sensitive response is cacheable")
				}
			})
		}
	}
}
