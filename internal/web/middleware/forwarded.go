package middleware

import (
	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"

	"github.com/Arman2122/p-ui/internal/util/proxytrust"
	"github.com/Arman2122/p-ui/internal/web/session"
)

// ForwardedHTTPS records whether the browser reached the panel over TLS:
// directly, or through a proxy inside trustedProxyCIDRs that says so.
func ForwardedHTTPS(trustedCIDRs string) gin.HandlerFunc {
	return func(c *gin.Context) {
		https := c.Request.TLS != nil ||
			proxytrust.ForwardedProtoHTTPS(c.Request.RemoteAddr, c.GetHeader("X-Forwarded-Proto"), trustedCIDRs)
		c.Set(session.RequestHTTPSKey, https)
		c.Next()
	}
}

// SecureCookiesBehindProxy marks the session cookie Secure on a request a
// trusted proxy carried over TLS, which the store's fixed options cannot know.
func SecureCookiesBehindProxy(opts sessions.Options) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !opts.Secure && c.GetBool(session.RequestHTTPSKey) {
			secured := opts
			secured.Secure = true
			sessions.Default(c).Options(secured)
		}
		c.Next()
	}
}
