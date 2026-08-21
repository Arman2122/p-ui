package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
)

// sessionEngine wires a store exactly as initRouter does, so the test exercises
// the real dependency rather than a stand-in for it.
func sessionEngine(t *testing.T, maxAgeSeconds int, apply bool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)

	store := cookie.NewStore([]byte("0123456789abcdef0123456789abcdef"))
	options := sessions.Options{Path: "/", HttpOnly: true, MaxAge: maxAgeSeconds}
	if apply {
		configureSessionStore(store, options)
	} else {
		// What the panel shipped: the options, and no re-key.
		store.Options(options)
	}

	engine := gin.New()
	engine.Use(sessions.Sessions("p-ui", store))
	engine.GET("/mint", func(c *gin.Context) {
		s := sessions.Default(c)
		s.Set("loginUser", 1)
		if err := s.Save(); err != nil {
			t.Errorf("save session: %v", err)
		}
		c.Status(http.StatusOK)
	})
	engine.GET("/whoami", func(c *gin.Context) {
		if sessions.Default(c).Get("loginUser") == nil {
			c.Status(http.StatusUnauthorized)
			return
		}
		c.Status(http.StatusOK)
	})
	return engine
}

func mintSessionCookie(t *testing.T, engine *gin.Engine) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/mint", nil))
	for _, c := range rec.Result().Cookies() {
		if c.Name == "p-ui" {
			return c
		}
	}
	t.Fatal("no session cookie was minted")
	return nil
}

func replay(t *testing.T, engine *gin.Engine, c *http.Cookie) int {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/whoami", nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	return rec.Code
}

/*
sessionMaxAge has to be a lifetime the server enforces, not a hint it hands the
browser. gin-contrib/sessions' Options() assigns the struct field and never
re-keys gorilla's securecookie codecs, which are pinned at construction to a
30-day default — so a cookie lifted out of a browser profile, a backup or a proxy
log replayed for thirty days no matter what the operator configured.

One second of lifetime, and a sleep past it. securecookie stamps whole seconds
and expires on t1 < now-maxAge, so the sleep clears two of them rather than
sitting on the boundary. The codec's own timestamp check is what must reject
the second replay.
*/
func TestSessionMaxAgeIsEnforcedByTheServer(t *testing.T) {
	engine := sessionEngine(t, 1, true)
	cookieValue := mintSessionCookie(t, engine)

	if code := replay(t, engine, cookieValue); code != http.StatusOK {
		t.Fatalf("a fresh session was rejected: %d", code)
	}

	time.Sleep(2100 * time.Millisecond)

	if code := replay(t, engine, cookieValue); code != http.StatusUnauthorized {
		t.Fatalf("a session past sessionMaxAge still authenticated (%d): the codecs were never re-keyed, so the server accepts it for 30 days", code)
	}
}

// The control: without applySessionMaxAge the very same cookie is still accepted,
// which is what the panel shipped.
func TestWithoutTheReKeyTheSameCookieOutlivesItsSetting(t *testing.T) {
	engine := sessionEngine(t, 1, false)
	cookieValue := mintSessionCookie(t, engine)

	time.Sleep(2100 * time.Millisecond)

	if code := replay(t, engine, cookieValue); code != http.StatusOK {
		t.Fatalf("expected the un-re-keyed store to keep accepting the cookie, got %d; if this changed, the fix above is no longer load-bearing", code)
	}
}

// Zero means "no expiry" to securecookie, not "expire immediately", so an unset
// or unreadable setting must leave the store's default alone.
func TestConfigureSessionStoreIgnoresANonPositiveAge(t *testing.T) {
	for _, seconds := range []int{0, -1} {
		engine := sessionEngine(t, seconds, true)
		cookieValue := mintSessionCookie(t, engine)
		if code := replay(t, engine, cookieValue); code != http.StatusOK {
			t.Fatalf("maxAge %d killed the session outright: %d", seconds, code)
		}
	}
}
