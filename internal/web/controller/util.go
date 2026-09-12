package controller

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/web/entity"
	"github.com/Arman2122/p-ui/internal/web/service"

	"github.com/gin-gonic/gin"
)

// getRemoteIp extracts the real IP address from the request headers or remote address.
func getRemoteIp(c *gin.Context) string {
	remoteIP, ok := extractTrustedIP(c.Request.RemoteAddr)
	if !ok {
		return "unknown"
	}

	if isTrustedProxy(remoteIP) {
		if ip, ok := extractTrustedIP(c.GetHeader("X-Real-IP")); ok {
			return ip
		}

		if ip, ok := clientIPFromForwardedFor(c.GetHeader("X-Forwarded-For")); ok {
			return ip
		}
	}

	return remoteIP
}

/*
clientIPFromForwardedFor reads X-Forwarded-For from the RIGHT.

The header is append-only by design: each proxy adds the peer it saw, so the
list is `<client>, <proxy1>, <proxy2>`. Everything a client sends arrives at the
LEFT — a request carrying `X-Forwarded-For: 1.2.3.4` reaches the panel as
`1.2.3.4, <the real client>`. Taking the leftmost entry therefore takes whatever
the caller wrote, which is why the login rate limiter keyed on a value the
attacker chose and a fresh key was one header away.

Walking right-to-left and skipping entries that are themselves trusted proxies
yields the nearest address this deployment did not vouch for. That is the
closest thing to a client identity the header can support.
*/
func clientIPFromForwardedFor(header string) (string, bool) {
	if header == "" {
		return "", false
	}
	parts := strings.Split(header, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		ip, ok := extractTrustedIP(parts[i])
		if !ok {
			continue
		}
		if isTrustedProxy(ip) {
			// One of our own hops; keep walking left for the client behind it.
			continue
		}
		return ip, true
	}
	// Every hop was a trusted proxy, so the header names no client. The direct
	// peer is the honest answer.
	return "", false
}

func isTrustedForwardedRequest(c *gin.Context) bool {
	remoteIP, ok := extractTrustedIP(c.Request.RemoteAddr)
	return ok && isTrustedProxy(remoteIP)
}

func isTrustedProxy(ip string) bool {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return false
	}

	trusted := trustedProxyCIDRs()
	for value := range strings.SplitSeq(trusted, ",") {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if prefix, err := netip.ParsePrefix(value); err == nil {
			if prefix.Contains(addr) {
				return true
			}
			continue
		}
		if proxyIP, err := netip.ParseAddr(value); err == nil && proxyIP.Unmap() == addr.Unmap() {
			return true
		}
	}
	return false
}

func trustedProxyCIDRs() (trusted string) {
	trusted = service.DefaultTrustedProxyCIDRs
	defer func() {
		_ = recover()
	}()
	settingService := service.SettingService{}
	if value, err := settingService.GetTrustedProxyCIDRs(); err == nil && strings.TrimSpace(value) != "" {
		trusted = value
	}
	return trusted
}

func extractTrustedIP(value string) (string, bool) {
	candidate := strings.TrimSpace(value)
	if candidate == "" {
		return "", false
	}

	if ip, ok := parseIPCandidate(candidate); ok {
		return ip.String(), true
	}

	if host, _, err := net.SplitHostPort(candidate); err == nil {
		if ip, ok := parseIPCandidate(host); ok {
			return ip.String(), true
		}
	}

	if strings.Count(candidate, ":") == 1 {
		if host, _, err := net.SplitHostPort(fmt.Sprintf("[%s]", candidate)); err == nil {
			if ip, ok := parseIPCandidate(host); ok {
				return ip.String(), true
			}
		}
	}

	return "", false
}

func parseIPCandidate(value string) (netip.Addr, bool) {
	ip, err := netip.ParseAddr(strings.TrimSpace(value))
	if err != nil {
		return netip.Addr{}, false
	}
	return ip.Unmap(), true
}

// jsonMsg sends a JSON response with a message and error status.
func jsonMsg(c *gin.Context, msg string, err error) {
	jsonMsgObj(c, msg, nil, err)
}

// jsonObj sends a JSON response with an object and error status.
func jsonObj(c *gin.Context, obj any, err error) {
	jsonMsgObj(c, "", obj, err)
}

func requestErrorContext(c *gin.Context) string {
	handler, loc := callerOutsideUtil()
	return fmt.Sprintf("[%s %s handler=%s %s]", c.Request.Method, c.Request.URL.Path, handler, loc)
}

func callerOutsideUtil() (string, string) {
	var pcs [12]uintptr
	n := runtime.Callers(2, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		base := filepath.Base(frame.File)
		if base != "util.go" {
			name := frame.Function
			if idx := strings.LastIndex(name, "/"); idx >= 0 {
				name = name[idx+1:]
			}
			return name, fmt.Sprintf("%s:%d", base, frame.Line)
		}
		if !more {
			break
		}
	}
	return "unknown", "unknown"
}

// jsonMsgObj sends a JSON response with a message, object, and error status.
/*
jsonMsgObjWarn answers a write that succeeded with something after it that did
not. The row is saved, so this is not a failure; but for a core the panel
converges directly there is no later restart that repairs it, so silence would
leave the operator with a green panel and a dead inbound.
*/
func jsonMsgObjWarn(c *gin.Context, msg string, obj any, warning string) {
	c.Set(activitySuccessKey, true)
	c.JSON(http.StatusOK, entity.Msg{Success: true, Msg: msg, Obj: obj, Warning: warning})
}

func jsonMsgObj(c *gin.Context, msg string, obj any, err error) {
	c.Set(activitySuccessKey, err == nil)
	m := entity.Msg{
		Obj: obj,
	}
	if err == nil {
		m.Success = true
		if msg != "" {
			m.Msg = msg
		}
	} else {
		m.Success = false
		ctx := requestErrorContext(c)
		fail := I18nWeb(c, "fail")
		errStr := err.Error()
		if errStr != "" {
			m.Msg = msg + " (" + errStr + ")"
			logger.Warningf("%s %s %s: %v", ctx, msg, fail, err)
		} else if msg != "" {
			m.Msg = msg
			logger.Warningf("%s %s %s", ctx, msg, fail)
		} else {
			m.Msg = I18nWeb(c, "somethingWentWrong")
			logger.Warningf("%s %s %s", ctx, m.Msg, fail)
		}
	}
	c.JSON(http.StatusOK, m)
}

// pendingObj flags what the save still owes: a backing node that was offline
// (mirrored when it reconnects) or a core restart. nil when nothing is pending.
func pendingObj(nodePending, restartPending bool) any {
	if !nodePending && !restartPending {
		return nil
	}
	out := gin.H{}
	if nodePending {
		out["nodePending"] = true
	}
	if restartPending {
		out["restartPending"] = true
	}
	return out
}

// applyDelayWarning tells the operator a save lands on the core's next pass,
// or nothing when it applied at once.
func applyDelayWarning(c *gin.Context, seconds int) string {
	if seconds <= 0 {
		return ""
	}
	return I18nWeb(c, "pages.inbounds.toasts.appliesWithin", "seconds=="+strconv.Itoa(seconds))
}

// pureJsonMsg sends a pure JSON message response with custom status code.
func pureJsonMsg(c *gin.Context, statusCode int, success bool, msg string) {
	c.Set(activitySuccessKey, success && statusCode < 400)
	c.JSON(statusCode, entity.Msg{
		Success: success,
		Msg:     msg,
	})
}

// isAjax checks if the request is an AJAX request.
func isAjax(c *gin.Context) bool {
	return c.GetHeader("X-Requested-With") == "XMLHttpRequest"
}

// resolveBulkEmails turns a bulk request's selection into emails: the list it
// carries, or every client matching the list page's filter it names instead.
func resolveBulkEmails(c *gin.Context, clients *service.ClientService, inbounds *service.InboundService, settings *service.SettingService, emails []string, filter *service.ClientPageParams) ([]string, bool) {
	if len(emails) > 0 || filter == nil {
		return emails, true
	}
	resolved, err := clients.ResolveEmails(inbounds, settings, *filter)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return nil, false
	}
	return resolved, true
}
