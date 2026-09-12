package controller

import (
	"errors"
	"strconv"
	"time"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/policy"
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
	RegisterPlanRoutes(g)
	g.GET("/list", a.list)
	g.GET("/get/:id", a.get)
	g.GET("/enforced/:email", a.enforced)

	g.POST("/add", a.add)
	g.POST("/update/:id", a.update)
	g.POST("/del/:id", a.del)
	g.POST("/assign", a.assign)
	g.POST("/pushAssignments", a.pushAssignments)
	g.POST("/pushBans", a.pushBans)

	g.GET("/bans", a.bans)
	g.POST("/bans/release", a.releaseBan)
	g.POST("/bans/releaseAll", a.releaseAllBans)
	g.POST("/bans/add", a.banAddress)
}

// bans lists every device-cap ban this panel decided, held here or on a node.
func (a *PolicyController) bans(c *gin.Context) {
	rows, err := a.policyService.ActiveBanViews(c.Request.Context())
	if err != nil {
		jsonMsg(c, I18nWeb(c, "get"), err)
		return
	}
	jsonObj(c, rows, nil)
}

// releaseBan frees one (client, source) wherever this panel banned it, now.
func (a *PolicyController) releaseBan(c *gin.Context) {
	var req struct {
		Email  string `json:"email"`
		Source string `json:"source"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Source == "" {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), errors.New("a source address is required"))
		return
	}
	released, err := a.policyService.ReleaseBan(c.Request.Context(), req.Email, req.Source, time.Now())
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonObj(c, map[string]int{"released": released}, nil)
}

func (a *PolicyController) releaseAllBans(c *gin.Context) {
	released, err := a.policyService.ReleaseAllBans(c.Request.Context(), time.Now())
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonObj(c, map[string]int{"released": released}, nil)
}

// banAddress is an operator's own ban: every local inbound port, for the given
// minutes or the operator's default.
func (a *PolicyController) banAddress(c *gin.Context) {
	var req struct {
		Source  string `json:"source"`
		Minutes int    `json:"minutes"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	row, err := a.policyService.BanAddress(c.Request.Context(), req.Source, req.Minutes, time.Now())
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonObj(c, row, nil)
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
	row, err := a.policyService.Get(id)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "get"), err)
		return
	}
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
	result, err := a.policyService.ApplyPlan(c.Request.Context(), service.PlanScope{}, req.Email, req.PolicyId, false)
	if result.NeedRestart {
		(&service.XrayService{}).SetToNeedRestart()
	}
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	notifyClientsChanged()
	jsonMsg(c, I18nWeb(c, "pages.inbounds.toasts.inboundClientUpdateSuccess"), nil)
}

// pushAssignments receives a master's resolved ladder for each client this node
// serves (see PolicyService.AcceptAssignments for the storage semantics).
func (a *PolicyController) pushAssignments(c *gin.Context) {
	var req struct {
		MasterGuid  string              `json:"masterGuid"`
		Assignments []policy.Assignment `json:"assignments"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	if err := a.policyService.AcceptAssignments(req.MasterGuid, req.Assignments); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonMsg(c, "success", nil)
}

// pushBans receives every device-cap ban a master holds for this node, the whole
// set, and the node then holds exactly that set from it (see AcceptBans).
func (a *PolicyController) pushBans(c *gin.Context) {
	var req struct {
		MasterGuid string            `json:"masterGuid"`
		Bans       []model.ClientBan `json:"bans"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	if err := a.policyService.AcceptBans(c.Request.Context(), req.MasterGuid, req.Bans, time.Now()); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonMsg(c, "success", nil)
}
