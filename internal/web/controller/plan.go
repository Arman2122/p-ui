package controller

import (
	"errors"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/service"
	"github.com/Arman2122/p-ui/internal/web/session"
)

type PlanController struct {
	policy   service.PolicyService
	reseller bool
}

// RegisterPlanRoutes adds catalogue actions to the existing administrator policies group.
func RegisterPlanRoutes(g *gin.RouterGroup) {
	a := &PlanController{}
	g.GET("/summary", a.summary)
	g.GET("/members/:id", a.members)
	g.POST("/archive/:id", a.archive)
	g.POST("/duplicate/:id", a.duplicate)
	g.POST("/apply", a.apply)
	g.POST("/reapply/:id", a.reapply)
}

// Reseller plans are private templates; budget-changing assignment stays in reseller client creation.
func NewResellerPlanController(g *gin.RouterGroup) *PlanController {
	a := &PlanController{reseller: true}
	g.Use(func(c *gin.Context) {
		if err := (&service.ResellerService{}).CanManagePlans(session.GetLoginUser(c)); err != nil {
			c.AbortWithStatusJSON(403, gin.H{"success": false, "msg": err.Error()})
			return
		}
		c.Next()
	})
	g.GET("/list", a.list)
	g.GET("/summary", a.summary)
	g.GET("/get/:id", a.get)
	g.GET("/members/:id", a.members)
	g.POST("/add", a.save)
	g.POST("/update/:id", a.save)
	g.POST("/archive/:id", a.archive)
	g.POST("/duplicate/:id", a.duplicate)
	g.POST("/del/:id", a.del)
	return a
}

func (a *PlanController) scope(c *gin.Context) service.PlanScope {
	if a.reseller {
		id := session.GetLoginUser(c).Id
		return service.PlanScope{OwnerUserID: &id}
	}
	return service.PlanScope{}
}

func planID(c *gin.Context) (int, error) {
	id, err := strconv.Atoi(c.Param("id"))
	if err == nil && id <= 0 {
		err = errors.New("positive plan id required")
	}
	return id, err
}

func planResponse(c *gin.Context, obj any, err error) {
	if err != nil {
		jsonMsg(c, I18nWeb(c, "somethingWentWrong"), err)
		return
	}
	jsonObj(c, obj, nil)
}

func (a *PlanController) list(c *gin.Context) {
	out, err := a.policy.ListPlans(a.scope(c))
	planResponse(c, out, err)
}

func (a *PlanController) summary(c *gin.Context) {
	out, err := a.policy.PlanSummary(a.scope(c))
	planResponse(c, out, err)
}

func (a *PlanController) get(c *gin.Context) {
	id, err := planID(c)
	if err != nil {
		planResponse(c, nil, err)
		return
	}
	out, err := a.policy.GetPlan(a.scope(c), id)
	planResponse(c, out, err)
}

func (a *PlanController) save(c *gin.Context) {
	row := &model.Policy{}
	if c.Param("id") != "" {
		id, err := planID(c)
		if err != nil {
			planResponse(c, nil, err)
			return
		}
		row, err = a.policy.GetPlan(a.scope(c), id)
		if err != nil {
			planResponse(c, nil, err)
			return
		}
	}
	id := row.Id
	if err := c.ShouldBindJSON(row); err != nil {
		planResponse(c, nil, err)
		return
	}
	row.Id = id
	out, err := a.policy.SavePlan(a.scope(c), row)
	planResponse(c, out, err)
}

func (a *PlanController) archive(c *gin.Context) {
	id, err := planID(c)
	if err != nil {
		planResponse(c, nil, err)
		return
	}
	var req struct {
		Archived *bool `json:"archived"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		planResponse(c, nil, err)
		return
	}
	if req.Archived == nil {
		planResponse(c, nil, errors.New("archived is required"))
		return
	}
	out, err := a.policy.ArchivePlan(a.scope(c), id, *req.Archived)
	planResponse(c, out, err)
}

func (a *PlanController) duplicate(c *gin.Context) {
	id, err := planID(c)
	if err != nil {
		planResponse(c, nil, err)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		planResponse(c, nil, err)
		return
	}
	out, err := a.policy.DuplicatePlan(a.scope(c), id, req.Name)
	planResponse(c, out, err)
}

func (a *PlanController) del(c *gin.Context) {
	id, err := planID(c)
	if err == nil {
		err = a.policy.DeletePlan(a.scope(c), id)
	}
	planResponse(c, id, err)
}

func (a *PlanController) members(c *gin.Context) {
	id, err := planID(c)
	if err != nil {
		planResponse(c, nil, err)
		return
	}
	page, e1 := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, e2 := strconv.Atoi(c.DefaultQuery("pageSize", "20"))
	if e1 != nil || e2 != nil {
		planResponse(c, nil, errors.New("invalid pagination"))
		return
	}
	out, err := a.policy.PlanMembers(a.scope(c), id, page, size)
	planResponse(c, out, err)
}

func (a *PlanController) apply(c *gin.Context) {
	var req struct {
		Email          string `json:"email"`
		PolicyID       int    `json:"policyId"`
		ClearOverrides bool   `json:"clearOverrides"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		planResponse(c, nil, err)
		return
	}
	out, err := a.policy.ApplyPlan(c.Request.Context(), a.scope(c), req.Email, req.PolicyID, req.ClearOverrides)
	if out.NeedRestart {
		(&service.XrayService{}).SetToNeedRestart()
	}
	notifyClientsChanged()
	planResponse(c, out, err)
}

func (a *PlanController) reapply(c *gin.Context) {
	id, err := planID(c)
	if err != nil {
		planResponse(c, nil, err)
		return
	}
	var req struct {
		ClearOverrides bool `json:"clearOverrides"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		planResponse(c, nil, err)
		return
	}
	out, err := a.policy.ReapplyPlan(c.Request.Context(), a.scope(c), id, req.ClearOverrides)
	if out.NeedRestart {
		(&service.XrayService{}).SetToNeedRestart()
	}
	notifyClientsChanged()
	planResponse(c, out, err)
}
