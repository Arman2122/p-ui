package controller

import (
	"github.com/gin-gonic/gin"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/web/service"
	"github.com/Arman2122/p-ui/internal/web/session"
)

func NewFleetClientSourceController(parent *gin.RouterGroup) {
	g := parent.Group("/clients", requireConfigAdmin)
	g.GET("/:id/source", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		view, err := service.ReadFleetClientSource(c.Request.Context(), database.GetDB(), session.GetLoginUser(c), c.Param("id"))
		jsonObj(c, view, err)
	})
}
