package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
)

/*
Behind a TLS-terminating proxy the panel serves cleartext, so the session
cookie was never Secure and HSTS was never sent, however the browser reached
it. A proxy inside trustedProxyCIDRs that says X-Forwarded-Proto: https now
gets both; any other peer saying the same gets neither. Feature gap S3.
*/
func TestForwardedHTTPSSecuresCookiesAndHSTSOnlyForATrustedProxy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(ForwardedHTTPS("127.0.0.1/32"))
	engine.Use(SecurityHeadersMiddleware(false, false))
	store := cookie.NewStore([]byte("forwarded-test-secret"))
	opts := sessions.Options{Path: "/", HttpOnly: true}
	store.Options(opts)
	engine.Use(sessions.Sessions("p-ui", store))
	engine.Use(SecureCookiesBehindProxy(opts))
	engine.GET("/login", func(c *gin.Context) {
		s := sessions.Default(c)
		s.Set("user", "admin")
		if err := s.Save(); err != nil {
			c.Status(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusOK)
	})

	tests := []struct {
		name       string
		remoteAddr string
		wantSecure bool
	}{
		{name: "trusted proxy", remoteAddr: "127.0.0.1:41234", wantSecure: true},
		{name: "any other peer", remoteAddr: "10.0.0.7:41234", wantSecure: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/login", nil)
			req.RemoteAddr = tc.remoteAddr
			req.Header.Set("X-Forwarded-Proto", "https")
			rec := httptest.NewRecorder()
			engine.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d", rec.Code)
			}
			gotHSTS := rec.Header().Get("Strict-Transport-Security") != ""
			if gotHSTS != tc.wantSecure {
				t.Errorf("HSTS sent = %v, want %v", gotHSTS, tc.wantSecure)
			}
			setCookie := strings.Join(rec.Header().Values("Set-Cookie"), "\n")
			if setCookie == "" {
				t.Fatal("no session cookie was written")
			}
			gotSecure := strings.Contains(setCookie, "Secure")
			if gotSecure != tc.wantSecure {
				t.Errorf("cookie Secure = %v, want %v (Set-Cookie: %q)", gotSecure, tc.wantSecure, setCookie)
			}
		})
	}
}
