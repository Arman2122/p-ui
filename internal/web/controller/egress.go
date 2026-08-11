package controller

import (
	"strconv"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/middleware"
	"github.com/Arman2122/p-ui/internal/web/service"

	"github.com/gin-gonic/gin"
)

// EgressController exposes the egress rows an L3 inbound can be sent out
// through. Attach is deliberately not part of the inbound update payload: a
// caller that never learned about egresses would silently detach one.
type EgressController struct {
	egressService service.EgressService
	xrayService   service.XrayService
}

func NewEgressController(g *gin.RouterGroup) *EgressController {
	a := &EgressController{}
	a.initRouter(g)
	return a
}

func (a *EgressController) initRouter(g *gin.RouterGroup) {
	g.GET("/list", a.list)
	g.GET("/get/:id", a.get)
	g.GET("/preflight", a.preflight)

	g.POST("/add", a.add)
	g.POST("/update/:id", a.update)
	g.POST("/del/:id", a.del)
	g.POST("/attach", a.attach)
}

func (a *EgressController) list(c *gin.Context) {
	rows, err := a.egressService.GetAll()
	if err != nil {
		jsonMsg(c, I18nWeb(c, "get"), err)
		return
	}
	jsonObj(c, rows, nil)
}

func (a *EgressController) get(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, I18nWeb(c, "get"), err)
		return
	}
	row, err := a.egressService.Get(id)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "get"), err)
		return
	}
	jsonObj(c, row, nil)
}

// preflight reports what stops this host carrying an egress — a foreign object
// in the reserved band, strict reverse-path filtering, forwarding left off.
func (a *EgressController) preflight(c *gin.Context) {
	report := a.egressService.Preflight(c.Request.Context())
	refusals := make([]string, 0, len(report.Refusals))
	for _, refusal := range report.Refusals {
		refusals = append(refusals, refusal.Error())
	}
	notes := report.Notes
	if notes == nil {
		notes = []string{}
	}
	jsonObj(c, gin.H{"ok": report.OK(), "refusals": refusals, "notes": notes}, nil)
}

func (a *EgressController) add(c *gin.Context) {
	row, ok := middleware.BindAndValidate[model.Egress](c)
	if !ok {
		return
	}
	created, err := a.egressService.Add(row)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonObj(c, created, nil)
	// The front is generated state, so the running core takes it through the
	// hot-apply path the pending-restart flag already drives.
	a.xrayService.SetToNeedRestart()
}

func (a *EgressController) update(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	row := &model.Egress{Id: id}
	if !middleware.BindAndValidateInto(c, row) {
		return
	}
	row.Id = id
	updated, err := a.egressService.Update(row)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonObj(c, updated, nil)
	a.xrayService.SetToNeedRestart()
}

func (a *EgressController) del(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	if err := a.egressService.Del(id); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonObj(c, id, nil)
	a.xrayService.SetToNeedRestart()
}

// attach points one inbound at an egress, or detaches it when egressId is 0. It
// costs one ip rule and never touches the core's config, so no restart is armed.
func (a *EgressController) attach(c *gin.Context) {
	var req struct {
		InboundId int `json:"inboundId" form:"inboundId"`
		EgressId  int `json:"egressId" form:"egressId"`
	}
	if err := c.ShouldBind(&req); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	if err := a.egressService.Attach(req.InboundId, req.EgressId); err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.inbounds.toasts.inboundUpdateSuccess"), nil)
}
