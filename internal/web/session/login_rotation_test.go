package session

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"

	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
A CSRF token minted before login must not survive it.

F209 was filed as session fixation. That mechanic does not exist here: the store
is gin-contrib/sessions/cookie, a gorilla CookieStore, which has no session id at
all — Save re-signs the entire cookie value, so anything an attacker planted
before login carries no user id afterwards and does not authenticate.

The half that IS real is the one the finding mentioned in passing. A CSRF token
is handed to anonymous visitors on demand, and it used to be carried into the
authenticated session unchanged. Whoever planted it could then forge requests as
the admin who logged in under it.

Rotated in SetLoginUser rather than at the two call sites, so the shared helper
is safe by default: it covers login and the credential-change path together.
*/

func sessionRouter(t *testing.T, handler gin.HandlerFunc) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(sessions.Sessions("p-ui", cookie.NewStore([]byte("test-secret-for-rotation"))))
	engine.GET("/probe", handler)
	return engine
}

// tokenAfter drives one request through the router and returns the CSRF token
// the session holds afterwards, plus the cookie so the next call can reuse it.
func tokenAfter(t *testing.T, engine *gin.Engine, cookieHeader string) (string, string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/probe", nil)
	if cookieHeader != "" {
		req.Header.Set("Cookie", cookieHeader)
	}
	engine.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("probe returned %d: %s", recorder.Code, recorder.Body.String())
	}
	next := cookieHeader
	if set := recorder.Header().Get("Set-Cookie"); set != "" {
		next = set
	}
	return recorder.Body.String(), next
}

func TestLoggingInMintsANewCSRFToken(t *testing.T) {
	// First request: an anonymous visitor is issued a token, exactly as the
	// /csrf-token endpoint does for the login page.
	anonEngine := sessionRouter(t, func(c *gin.Context) {
		token, err := EnsureCSRFToken(c)
		if err != nil {
			t.Fatalf("mint anonymous token: %v", err)
		}
		c.String(http.StatusOK, token)
	})
	planted, cookieHeader := tokenAfter(t, anonEngine, "")
	if planted == "" {
		t.Fatal("no anonymous token was issued, so there is nothing this test can prove")
	}

	// Second request: the same session logs in, then reports its token.
	loginEngine := sessionRouter(t, func(c *gin.Context) {
		if err := SetLoginUser(c, &model.User{Id: 1, LoginEpoch: 7}); err != nil {
			t.Fatalf("SetLoginUser: %v", err)
		}
		token, err := EnsureCSRFToken(c)
		if err != nil {
			t.Fatalf("read token after login: %v", err)
		}
		c.String(http.StatusOK, token)
	})
	after, _ := tokenAfter(t, loginEngine, cookieHeader)

	if after == "" {
		t.Fatal("the authenticated session holds no CSRF token at all")
	}
	if after == planted {
		t.Fatalf("the pre-login token survived authentication (%q); whoever planted it can forge this admin's requests", planted)
	}
}

// And the login itself must still take: rotating the token cannot cost the
// session its identity, or every login would land back on the login page.
func TestLoggingInStillStoresTheUser(t *testing.T) {
	engine := sessionRouter(t, func(c *gin.Context) {
		if err := SetLoginUser(c, &model.User{Id: 42, LoginEpoch: 3}); err != nil {
			t.Fatalf("SetLoginUser: %v", err)
		}
		s := sessions.Default(c)
		id, _ := sessionUserID(s.Get(loginUserKey))
		if id != 42 {
			t.Fatalf("session user id = %v, want 42", s.Get(loginUserKey))
		}
		// Compared as a formatted value: the epoch's stored type is the model's,
		// and this test is about the login taking, not about that type.
		if got := fmt.Sprintf("%v", s.Get(loginEpochKey)); got != "3" {
			t.Fatalf("session login epoch = %q, want 3", got)
		}
		c.String(http.StatusOK, "ok")
	})
	tokenAfter(t, engine, "")
}
