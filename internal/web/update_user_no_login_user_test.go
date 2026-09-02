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

/*
updateUser must not dereference a login user the request never had.

checkAPIAuth admits a bearer-token caller and attaches the first user only when
GetFirstUser succeeds; with no user row it still sets api_authed and continues.
updateUser then read user.Username off a nil pointer. gin recovers the panic
into a 500, so nothing crashed -- but a request that should have been refused
answered with a stack trace instead. F213.
*/
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
	// The one state that leaves api_authed set and no user attached.
	if err := database.GetDB().Where("1 = 1").Delete(&model.User{}).Error; err != nil {
		t.Fatalf("delete users: %v", err)
	}

	form := url.Values{"oldUsername": {"admin"}, "oldPassword": {"x"}, "newUsername": {"admin"}, "newPassword": {"y"}}
	req := httptest.NewRequest(http.MethodPost, "/panel/api/setting/updateUser", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code == http.StatusInternalServerError {
		t.Fatalf("updateUser answered 500: the nil login user was dereferenced\n%s", rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"success":false`) {
		t.Fatalf("status %d, body %s: want an explicit refusal", rec.Code, rec.Body.String())
	}
}
