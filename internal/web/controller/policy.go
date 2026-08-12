package controller

import (
	"strconv"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/middleware"
	"github.com/Arman2122/p-ui/internal/web/service"

	"github.com/gin-gonic/gin"
)

/*
PolicyController exposes the speed ladders and who is on which one.

Assignment is its own endpoint rather than a field on the client payload for the
reason attach is not part of the inbound payload: a caller written before plans
existed would silently unassign every client it updated.
*/
type PolicyController struct {
	policyService service.PolicyService
}

func NewPolicyController(g *gin.RouterGroup) *PolicyController {
	a := &PolicyController{}
	a.initRouter(g)
	return a
}

func (a *PolicyController) initRouter(g *gin.RouterGroup) {
	g.GET("/list", a.list)
	g.GET("/get/:id", a.get)
	g.GET("/enforced/:email", a.enforced)

	g.POST("/add", a.add)
	g.POST("/update/:id", a.update)
	g.POST("/del/:id", a.del)
	g.POST("/assign", a.assign)
}

func (a *PolicyController) list(c *gin.Context) {
	rows, err := a.policyService.GetAll()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "get"), err)
		return
	}
	jsonObj(c, rows, nil)
}

func (a *PolicyController) get(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, I18nWeb(c, "get"), err)
		return
	}
	row, err := a.policyService.Get(id)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "get"), err)
		return
	}
	jsonObj(c, row, nil)
}

// enforced answers with what the KERNEL holds for one client, never with what the
// panel pushed: only a readback can show a limit that never landed.
func (a *PolicyController) enforced(c *gin.Context) {
	view, err := a.policyService.EnforcedFor(c.Request.Context(), c.Param("email"))
	if err != nil {
		jsonMsg(c, I18nWeb(c, "get"), err)
		return
	}
	jsonObj(c, view, nil)
}

func (a *PolicyController) add(c *gin.Context) {
	row, ok := middleware.BindAndValidate[model.Policy](c)
	if !ok {
		return
	}
	created, err := a.policyService.Add(row)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonObj(c, created, nil)
}

func (a *PolicyController) update(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	row := &model.Policy{Id: id}
	if !middleware.BindAndValidateInto(c, row) {
		return
	}
	row.Id = id
	updated, err := a.policyService.Update(row)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonObj(c, updated, nil)
}

// del leaves every assignment row behind with a null plan, so the operator can
// see which clients lost theirs instead of finding out from a customer.
func (a *PolicyController) del(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	if err := a.policyService.Del(id); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonObj(c, id, nil)
}

// assign puts one client on a plan, or takes them off it with policyId 0. It
// changes no core's config, so nothing is restarted and no connection drops.
func (a *PolicyController) assign(c *gin.Context) {
	var req struct {
		Email    string `json:"email" form:"email"`
		PolicyId int    `json:"policyId" form:"policyId"`
	}
	if err := c.ShouldBind(&req); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	if err := a.policyService.Assign(req.Email, req.PolicyId); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.inbounds.toasts.inboundClientUpdateSuccess"), nil)
}
