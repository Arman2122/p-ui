package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/robfig/cron/v3"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/util/crypto"
	"github.com/Arman2122/p-ui/internal/web/global"
)

// A bearer token without an administrator must be refused before updateUser runs.
// Keep the explicit JSON refusal contract while preventing the historical nil dereference. F213.
func TestUpdateUserWithNoLoginUserIsRefusedNotPanicked(t *testing.T) {
	initTestDB(t)
	previous := global.GetWebServer()
	s := NewServer()
	s.cron = cron.New(cron.WithSeconds())
	global.SetWebServer(s)
	t.Cleanup(func() { global.SetWebServer(previous) })
	engine, err := s.initRouter()
	if err != nil {
		t.Fatalf("initRouter: %v", err)
	}

	const token = "smoke-token-for-f213"
	if err := database.GetDB().Create(&model.ApiToken{Name: "f213", Token: crypto.HashTokenSHA256(token), Enabled: true}).Error; err != nil {
		t.Fatalf("mint token: %v", err)
	}
	// The token still exists, but there is no administrator who can authenticate with it.
	if err := database.GetDB().Where("1 = 1").Delete(&model.User{}).Error; err != nil {
		t.Fatalf("delete users: %v", err)
	}

	form := url.Values{"oldUsername": {"admin"}, "oldPassword": {"x"}, "newUsername": {"admin"}, "newPassword": {"y"}}
	req := httptest.NewRequest(http.MethodPost, "/panel/api/setting/updateUser", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("updateUser status=%d want401\n%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"success":false`) {
		t.Fatalf("status %d, body %s: want an explicit refusal", rec.Code, rec.Body.String())
	}
}
