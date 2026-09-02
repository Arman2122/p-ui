package sub

import (
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/util/proxytrust"
)

var warnSuppressedForwardedOnce sync.Once

var forwardedHeaderNames = [...]string{"X-Forwarded-Host", "X-Forwarded-Proto", "X-Real-IP"}

/*
forwardedHeadersTrusted reports whether this peer may set X-Forwarded-*.

trustedProxyCIDRs is honoured at EVERY value, including its shipped default and
including empty. It used to short-circuit to "trusted" for both, so a panel
whose operator had never touched the setting accepted X-Forwarded-Host from any
address on the internet — and the subscription URLs it generates are built from
that host.

The default is 127.0.0.1/32,::1/128, which is exactly the same-box reverse proxy
almost every deployment runs, so honouring it keeps the common case working. A
proxy on another host now has to be declared, and warnSuppressedForwardedHeaders
already tells the operator precisely that when the links look wrong.

Unreadable settings and a malformed CIDR list are refusals, not passes: this
question is "is the peer inside a boundary", and not knowing the boundary is not
a yes.
*/
func (s *SubService) forwardedHeadersTrusted(c *gin.Context) (trusted bool) {
	if !hasForwardedHeaders(c) {
		return true
	}

	defer func() {
		if recover() != nil {
			trusted = false
		}
	}()

	configured, err := s.settingService.GetTrustedProxyCIDRs()
	if err != nil {
		logger.Warning("sub: cannot read trustedProxyCIDRs, ignoring forwarded headers:", err)
		return false
	}
	configured = strings.TrimSpace(configured)
	if configured == "" {
		return false
	}
	return remoteAddrInCIDRs(c.Request.RemoteAddr, configured)
}

func hasForwardedHeaders(c *gin.Context) bool {
	for _, name := range forwardedHeaderNames {
		if c.GetHeader(name) != "" {
			return true
		}
	}
	return false
}

func warnSuppressedForwardedHeaders(c *gin.Context) {
	present := make([]string, 0, 3)
	for _, name := range forwardedHeaderNames {
		if c.GetHeader(name) != "" {
			present = append(present, name)
		}
	}
	if len(present) == 0 {
		return
	}
	headers := strings.Join(present, ", ")
	logger.Debugf("sub: ignoring %s from %s, which is outside trustedProxyCIDRs", headers, c.Request.RemoteAddr)
	warnSuppressedForwardedOnce.Do(func() {
		logger.Warningf("sub: ignoring %s from %s because it is outside trustedProxyCIDRs; subscription URLs will use the request host. Add the proxy to that setting, or set subURI, if the generated links look wrong.", headers, c.Request.RemoteAddr)
	})
}

func remoteAddrInCIDRs(remoteAddr, cidrs string) bool {
	return proxytrust.RemoteAddrInCIDRs(remoteAddr, cidrs)
}
