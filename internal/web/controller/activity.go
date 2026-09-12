package controller

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Arman2122/p-ui/internal/web/service"
	"github.com/Arman2122/p-ui/internal/web/session"
)

type ActivityController struct{ service service.ActivityService }

func NewActivityController(g *gin.RouterGroup) *ActivityController {
	a := &ActivityController{}
	g.GET("/activity", a.list)
	g.GET("/activity/alerts", a.alerts)
	g.POST("/activity/read", a.read)
	return a
}

func (a *ActivityController) alerts(c *gin.Context) {
	user := session.GetLoginUser(c)
	if user == nil {
		c.AbortWithStatus(401)
		return
	}
	secure := c.Request.TLS != nil || (isTrustedForwardedRequest(c) && strings.EqualFold(c.GetHeader("X-Forwarded-Proto"), "https"))
	rows, err := a.service.Alerts(c.Request.Context(), user.Id, session.IsAdminUser(user), secure)
	jsonObj(c, rows, err)
}

func (a *ActivityController) list(c *gin.Context) {
	user := session.GetLoginUser(c)
	if user == nil {
		c.AbortWithStatus(401)
		return
	}
	before, _ := strconv.ParseInt(c.Query("before"), 10, 64)
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "100"))
	page, err := a.service.List(c.Request.Context(), user.Id, session.IsAdminUser(user), before, limit)
	jsonObj(c, page, err)
}

func (a *ActivityController) read(c *gin.Context) {
	user := session.GetLoginUser(c)
	if user == nil {
		c.AbortWithStatus(401)
		return
	}
	var req struct {
		ID        int64 `json:"id"`
		ThroughID int64 `json:"throughId"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonMsg(c, "", err)
		return
	}
	jsonMsg(c, "", a.service.MarkRead(c.Request.Context(), user.Id, session.IsAdminUser(user), req.ID, req.ThroughID))
}
