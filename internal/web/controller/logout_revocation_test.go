package controller

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	panelservice "github.com/Arman2122/p-ui/internal/web/service/panel"
	"github.com/Arman2122/p-ui/internal/web/session"
)

/*
Logout has to revoke, not just tidy the browser.

ClearSession expires the copy in the caller's browser and nothing else, so a
cookie already captured — a shared machine, a backup, a proxy log — kept
authenticating for its full lifetime. The login epoch is the panel's one
server-side revocation, and logout never moved it, so "log out" on a machine you
no longer trust revoked nothing at all.
*/
func TestLogoutRevokesACookieAlreadyCaptured(t *testing.T) {
	initTestDB(t)
	gin.SetMode(gin.TestMode)

	admin := &model.User{Username: "logout-epoch-admin", Password: "x"}
	if err := database.GetDB().Create(admin).Error; err != nil {
		t.Fatalf("create admin: %v", err)
	}

	controller := &IndexController{}
	store := cookie.NewStore([]byte("0123456789abcdef0123456789abcdef"))
	store.Options(sessions.Options{Path: "/", HttpOnly: true})

	engine := gin.New()
	engine.Use(sessions.Sessions("p-ui", store))
	engine.GET("/login", func(c *gin.Context) {
		if err := session.SetLoginUser(c, admin); err != nil {
			t.Errorf("SetLoginUser: %v", err)
		}
		c.Status(http.StatusOK)
	})
	engine.GET("/logout", controller.logout)
	engine.GET("/whoami", func(c *gin.Context) {
		if session.GetLoginUser(c) == nil {
			c.Status(http.StatusUnauthorized)
			return
		}
		c.Status(http.StatusOK)
	})

	call := func(path string, jar *http.Cookie) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if jar != nil {
			req.AddCookie(jar)
		}
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		return rec
	}

	minted := call("/login", nil)
	var captured *http.Cookie
	for _, c := range minted.Result().Cookies() {
		if c.Name == "p-ui" {
			captured = c
		}
	}
	if captured == nil {
		t.Fatal("no session cookie was minted")
	}
	if code := call("/whoami", captured).Code; code != http.StatusOK {
		t.Fatalf("the captured cookie did not authenticate before logout: %d", code)
	}

	// A second browser logs out. The captured copy is untouched by that, which
	// is exactly the case ClearSession alone cannot answer.
	if code := call("/logout", captured).Code; code != http.StatusOK {
		t.Fatalf("logout returned %d", code)
	}

	if code := call("/whoami", captured).Code; code != http.StatusUnauthorized {
		t.Fatalf("the captured cookie still authenticates after logout (%d): nothing was revoked server-side", code)
	}

	var epoch int64
	if err := database.GetDB().Model(model.User{}).
		Where("id = ?", admin.Id).Pluck("login_epoch", &epoch).Error; err != nil {
		t.Fatalf("read login_epoch: %v", err)
	}
	if epoch != admin.LoginEpoch+1 {
		t.Fatalf("login_epoch = %d, want %d", epoch, admin.LoginEpoch+1)
	}
}

// A logout whose revocation fails must still clear the browser's copy, or the
// operator is left looking signed in because the database hiccuped.
func TestLogoutStillClearsTheBrowserWhenRevocationFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// No database at all: BumpLoginEpoch cannot succeed.
	_ = database.CloseDB()

	controller := &IndexController{}
	store := cookie.NewStore([]byte("0123456789abcdef0123456789abcdef"))
	store.Options(sessions.Options{Path: "/", HttpOnly: true})

	engine := gin.New()
	engine.Use(sessions.Sessions("p-ui", store))
	engine.GET("/logout", controller.logout)

	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/logout", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("logout returned %d; it must answer even when revocation fails", rec.Code)
	}
	cleared := false
	for _, c := range rec.Result().Cookies() {
		if c.Name == "p-ui" && c.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Fatal("the browser's copy was not expired, so the operator still looks signed in")
	}
}

// A fresh test process exercises the nil-handle branch regardless of suite order.
func TestRevokingSessionsWithNoDatabaseIsAnErrorNotAPanic(t *testing.T) {
	const childKey = "PUI_TEST_EMPTY_SESSION_DB"
	if os.Getenv(childKey) == "1" {
		if database.GetDB() != nil {
			t.Fatal("fresh process unexpectedly opened a database")
		}
		const want = "panel: no database connection to revoke sessions with"
		if err := (&panelservice.UserService{}).BumpLoginEpoch(); err == nil || err.Error() != want {
			t.Fatalf("revoke error=%v want%q", err, want)
		}
		return
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestRevokingSessionsWithNoDatabaseIsAnErrorNotAPanic$")
	cmd.Env = append(os.Environ(), childKey+"=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("nil-database child: %v\n%s", err, output)
	}
}
