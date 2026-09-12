package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/entity"
	"github.com/Arman2122/p-ui/internal/web/service"
	"github.com/Arman2122/p-ui/internal/web/session"
)

type CoreConfigController struct{ service service.CoreConfigService }

type CoreConfigValidateForm struct {
	Kind     string `json:"kind" form:"kind" example:"tuic"`
	Document string `json:"document" form:"document" binding:"required" example:"{}"`
}

func NewCoreConfigController(g *gin.RouterGroup) *CoreConfigController {
	a := &CoreConfigController{}
	g = g.Group("/core-config", requireConfigAdmin)
	g.GET("/:id", a.get)
	g.POST("/:id/validate", a.validate)
	return a
}

func requireConfigAdmin(c *gin.Context) {
	if !session.IsAdmin(c) || c.GetString("api_scope") == model.ApiTokenScopeRead {
		c.AbortWithStatusJSON(http.StatusForbidden, entity.Msg{Success: false, Msg: "administrator access is required for configuration and backups"})
		return
	}
	c.Next()
}

func (a *CoreConfigController) get(c *gin.Context) {
	view, err := a.service.Get(c.Param("id"))
	c.Header("Cache-Control", "no-store")
	jsonObj(c, view, err)
}

func (a *CoreConfigController) validate(c *gin.Context) {
	var form CoreConfigValidateForm
	if err := c.ShouldBind(&form); err != nil {
		jsonObj(c, nil, err)
		return
	}
	jsonObj(c, nil, a.service.Validate(c.Request.Context(), c.Param("id"), form.Kind, form.Document))
}
