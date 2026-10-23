package controller

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/web/service"
	"github.com/Arman2122/p-ui/internal/web/session"
)

const activitySuccessKey = "activity.mutationSuccess"

func auditOperation(path string) (string, string) {
	_, route, ok := strings.Cut(path, "/panel/api/")
	if !ok {
		return "", ""
	}
	parts := strings.Split(route, "/")
	switch route {
	case "fleet/network/defaults", "fleet/network/nodes/:id", "fleet/network/nodes/:id/assets", "fleet/network/publish",
		"fleet/placement/nodes/:id", "fleet/placement/rules", "fleet/placement/default", "fleet/placement/clients/:id", "fleet/clients/:id/reset-schedule", "fleet/clients/:id/reset-schedule/cancel":
		return "fleet", "update"
	case "resellers", "reseller/clients", "reseller/tokens", "fleet/clients":
		return parts[0], "create"
	case "reseller/password", "backups/schedule":
		return parts[0], "update"
	case "backups/upload":
		return parts[0], "import"
	}
	if len(parts) < 2 || parts[0] == "activity" {
		return "", ""
	}
	for _, part := range parts[1:] {
		part = strings.ReplaceAll(strings.TrimPrefix(strings.ToLower(part), "bulk"), "-", "")
		if strings.HasPrefix(part, ":") {
			continue
		}
		if part == "restore" {
			return parts[0], "import"
		}
		for _, prefix := range []string{"create", "add", "update", "set", "reset", "delete", "del", "remove", "rename", "assign", "reapply", "apply", "import", "install", "restart", "stop", "suspend", "topup", "extend", "archive", "duplicate", "reorder", "move", "release"} {
			if strings.HasPrefix(part, prefix) {
				return parts[0], prefix
			}
		}
	}
	return "", ""
}

func ActivityAuditMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead || c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}
		resource, operation := auditOperation(c.FullPath())
		user := session.GetLoginUser(c)
		c.Next()
		if resource == "" || user == nil || !c.GetBool(activitySuccessKey) {
			return
		}
		params := map[string]string{"actor": user.Username, "operation": operation, "resource": resource, "target": ""}
		for _, key := range []string{"email", "name", "id"} {
			if value := c.Param(key); value != "" {
				params["target"] = value
				break
			}
		}
		ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 3*time.Second)
		defer cancel()
		audience := 0
		if !session.IsAdminUser(user) {
			audience = user.Id
		}
		_, err := (service.ActivityService{}).Record(ctx, model.ActivityEvent{
			AudienceUserID: audience, ActorUserID: user.Id,
			Kind: "audit." + resource + "." + operation, Level: "info", TitleKey: "events.audit.title", MessageKey: "events.audit.body",
		}, params)
		if err != nil {
			logger.Warning("record activity audit:", err)
		}
	}
}
