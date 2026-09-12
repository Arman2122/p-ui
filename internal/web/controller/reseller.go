package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/web/entity"
	"github.com/Arman2122/p-ui/internal/web/service"
	"github.com/Arman2122/p-ui/internal/web/session"
)

type ResellerController struct{ service service.ResellerService }

func NewResellerController(api *gin.RouterGroup) *ResellerController {
	a := &ResellerController{}
	api.GET("/me", a.me)
	g := api.Group("/reseller")
	g.Use(a.requireReseller)
	g.GET("/dashboard", a.dashboard)
	g.GET("/inbounds", a.inbounds)
	g.GET("/clients", a.clients)
	g.POST("/clients", a.createClient)
	g.POST("/clients/:id/update", a.updateClient)
	g.POST("/clients/:id/delete", a.deleteClient)
	g.POST("/clients/:id/reset", a.resetClient)
	g.GET("/clients/:id/links", a.links)
	g.GET("/tokens", a.tokens)
	g.POST("/tokens", a.createToken)
	g.POST("/tokens/:id/delete", a.deleteToken)
	g.POST("/password", a.password)
	admin := api.Group("/resellers")
	admin.Use(requireAdmin)
	admin.GET("", a.list)
	admin.GET("/:id", a.get)
	admin.GET("/:id/clients", a.adminClients)
	admin.POST("", a.create)
	admin.POST("/:id/update", a.update)
	admin.POST("/:id/suspend", a.suspend)
	admin.POST("/:id/top-up", a.topUp)
	return a
}

func requireAdmin(c *gin.Context) {
	if !session.IsAdmin(c) {
		c.AbortWithStatusJSON(http.StatusForbidden, entity.Msg{Success: false, Msg: "administrator access required"})
		return
	}
	c.Next()
}

func (a *ResellerController) requireReseller(c *gin.Context) {
	if err := a.service.CheckAccess(session.GetLoginUser(c)); err != nil {
		c.AbortWithStatusJSON(http.StatusForbidden, entity.Msg{Success: false, Msg: err.Error()})
		return
	}
	c.Next()
}

func resellerResponse(c *gin.Context, obj any, err error) {
	if err == nil {
		jsonObj(c, obj, nil)
		return
	}
	status := http.StatusBadRequest
	if errors.Is(err, service.ErrResellerForbidden) {
		status = http.StatusForbidden
	}
	if errors.Is(err, service.ErrResellerBudget) {
		status = http.StatusConflict
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		status = http.StatusNotFound
	}
	c.JSON(status, entity.Msg{Success: false, Msg: err.Error(), Obj: obj})
}

func resellerID(c *gin.Context) int { id, _ := strconv.Atoi(c.Param("id")); return id }
func (a *ResellerController) me(c *gin.Context) {
	obj, err := a.service.Me(session.GetLoginUser(c))
	resellerResponse(c, obj, err)
}

func (a *ResellerController) list(c *gin.Context) {
	obj, err := a.service.List(session.GetLoginUser(c))
	resellerResponse(c, obj, err)
}

func (a *ResellerController) get(c *gin.Context) {
	obj, err := a.service.Get(session.GetLoginUser(c), resellerID(c))
	resellerResponse(c, obj, err)
}
func (a *ResellerController) create(c *gin.Context) { a.save(c, 0) }
func (a *ResellerController) update(c *gin.Context) {
	if resellerID(c) <= 0 {
		resellerResponse(c, nil, service.ErrResellerInvalid)
		return
	}
	a.save(c, resellerID(c))
}

func (a *ResellerController) save(c *gin.Context, id int) {
	var in service.ResellerInput
	if err := c.ShouldBindJSON(&in); err != nil {
		resellerResponse(c, nil, service.ErrResellerInvalid)
		return
	}
	obj, err := a.service.Save(session.GetLoginUser(c), id, in)
	resellerResponse(c, obj, err)
}

func (a *ResellerController) suspend(c *gin.Context) {
	var in service.ResellerSuspendRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		resellerResponse(c, nil, service.ErrResellerInvalid)
		return
	}
	if in.Suspended == nil {
		resellerResponse(c, nil, service.ErrResellerInvalid)
		return
	}
	resellerResponse(c, nil, a.service.Suspend(session.GetLoginUser(c), resellerID(c), *in.Suspended))
}

func (a *ResellerController) topUp(c *gin.Context) {
	var in service.ResellerTopUpRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		resellerResponse(c, nil, service.ErrResellerInvalid)
		return
	}
	resellerResponse(c, nil, a.service.TopUp(session.GetLoginUser(c), resellerID(c), in.Traffic, in.Days, in.Clients))
}

func (a *ResellerController) dashboard(c *gin.Context) {
	obj, err := a.service.Dashboard(session.GetLoginUser(c))
	resellerResponse(c, obj, err)
}

func (a *ResellerController) inbounds(c *gin.Context) {
	obj, err := a.service.Inbounds(session.GetLoginUser(c))
	resellerResponse(c, obj, err)
}

func (a *ResellerController) clients(c *gin.Context)      { a.clientPage(c, session.GetLoginUser(c).Id) }
func (a *ResellerController) adminClients(c *gin.Context) { a.clientPage(c, resellerID(c)) }
func (a *ResellerController) clientPage(c *gin.Context, id int) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "50"))
	obj, err := a.service.Clients(session.GetLoginUser(c), id, page, size, c.Query("search"))
	resellerResponse(c, obj, err)
}

func (a *ResellerController) createClient(c *gin.Context) {
	var in service.ResellerClientInput
	if err := c.ShouldBindJSON(&in); err != nil {
		resellerResponse(c, nil, service.ErrResellerInvalid)
		return
	}
	obj, err := a.service.CreateClient(session.GetLoginUser(c), in)
	resellerResponse(c, obj, err)
}

func (a *ResellerController) updateClient(c *gin.Context) {
	var in service.ResellerClientInput
	if err := c.ShouldBindJSON(&in); err != nil {
		resellerResponse(c, nil, service.ErrResellerInvalid)
		return
	}
	obj, err := a.service.UpdateClient(session.GetLoginUser(c), resellerID(c), in)
	resellerResponse(c, obj, err)
}

func (a *ResellerController) deleteClient(c *gin.Context) {
	resellerResponse(c, nil, a.service.DeleteClient(session.GetLoginUser(c), resellerID(c)))
}

func (a *ResellerController) resetClient(c *gin.Context) {
	resellerResponse(c, nil, a.service.ResetClient(session.GetLoginUser(c), resellerID(c)))
}

func (a *ResellerController) links(c *gin.Context) {
	obj, err := a.service.ClientLinks(session.GetLoginUser(c), resellerID(c), c.Request.Host)
	resellerResponse(c, obj, err)
}

func (a *ResellerController) tokens(c *gin.Context) {
	obj, err := a.service.Tokens(session.GetLoginUser(c))
	resellerResponse(c, obj, err)
}

func (a *ResellerController) createToken(c *gin.Context) {
	var in service.ResellerTokenRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		resellerResponse(c, nil, service.ErrResellerInvalid)
		return
	}
	obj, err := a.service.CreateToken(session.GetLoginUser(c), in.Name, in.ExpiresAt)
	resellerResponse(c, obj, err)
}

func (a *ResellerController) deleteToken(c *gin.Context) {
	resellerResponse(c, nil, a.service.DeleteToken(session.GetLoginUser(c), resellerID(c)))
}

func (a *ResellerController) password(c *gin.Context) {
	var in service.ResellerPasswordRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		resellerResponse(c, nil, service.ErrResellerInvalid)
		return
	}
	resellerResponse(c, nil, a.service.ChangePassword(session.GetLoginUser(c), in.Current, in.Password))
}
