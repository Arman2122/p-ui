package controller

import (
	"context"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/middleware"
	"github.com/Arman2122/p-ui/internal/web/service"
)

// RoutingController owns operator routing intent. A rule names inbounds by id
// and a destination, and the compile decides which mechanism realises it — so
// this is the one screen that routes any protocol to any protocol.
type RoutingController struct {
	routingService service.RoutingService
	xrayService    service.XrayService
}

func NewRoutingController(g *gin.RouterGroup) *RoutingController {
	a := &RoutingController{}
	a.initRouter(g)
	return a
}

func (a *RoutingController) initRouter(g *gin.RouterGroup) {
	g.GET("/rules", a.rules)
	g.GET("/subjects", a.subjects)

	g.POST("/rules", a.add)
	g.POST("/rules/order", a.reorder)
	g.POST("/rules/:id", a.update)
	g.POST("/rules/:id/del", a.del)
}

func (a *RoutingController) rules(c *gin.Context) {
	rules, err := a.routingService.Rules()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonObj(c, rules, nil)
}

// subjects answers which inbounds a rule may name, and why not when it may not.
func (a *RoutingController) subjects(c *gin.Context) {
	subjects, err := a.routingService.SubjectViews(context.Background())
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonObj(c, subjects, nil)
}

func (a *RoutingController) add(c *gin.Context) {
	rule, ok := middleware.BindAndValidate[model.RoutingRule](c)
	if !ok {
		return
	}
	created, err := a.routingService.Add(context.Background(), rule)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonObj(c, created, nil)
	a.xrayService.SetToNeedRestart()
}

func (a *RoutingController) update(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	rule, ok := middleware.BindAndValidate[model.RoutingRule](c)
	if !ok {
		return
	}
	rule.Id = id
	updated, err := a.routingService.Update(context.Background(), rule)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonObj(c, updated, nil)
	a.xrayService.SetToNeedRestart()
}

func (a *RoutingController) del(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	if err := a.routingService.Del(context.Background(), id); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.inbounds.toasts.inboundUpdateSuccess"), nil)
	a.xrayService.SetToNeedRestart()
}

// reorder takes the whole id list, so first-match order is set in one write
// rather than by a sequence of swaps a failure could leave half-applied.
func (a *RoutingController) reorder(c *gin.Context) {
	var req struct {
		Ids []int `json:"ids" form:"ids"`
	}
	if err := c.ShouldBind(&req); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	if err := a.routingService.Reorder(context.Background(), req.Ids); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.inbounds.toasts.inboundUpdateSuccess"), nil)
	a.xrayService.SetToNeedRestart()
}
