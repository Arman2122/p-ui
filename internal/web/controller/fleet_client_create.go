package controller

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/Arman2122/p-ui/internal/web/entity"
	"github.com/Arman2122/p-ui/internal/web/middleware"
	"github.com/Arman2122/p-ui/internal/web/service"
	"github.com/Arman2122/p-ui/internal/web/session"
)

func NewFleetClientCreateController(parent *gin.RouterGroup) {
	g := parent.Group("/clients", requireConfigAdmin)
	g.GET("/capabilities", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		jsonObj(c, service.FleetClientCreationCapabilities(session.GetLoginUser(c)), nil)
	})
	g.GET("/creation/:operationId", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		result, err := service.ReadFleetClientCreation(c.Request.Context(), session.GetLoginUser(c), c.Param("operationId"))
		jsonObj(c, result, err)
	})
	g.POST("", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
		request, ok := middleware.BindAndValidate[entity.ClientCreatePayload](c)
		if !ok {
			return
		}
		result, err := service.CreateFleetClient(c.Request.Context(), session.GetLoginUser(c), *request)
		jsonObj(c, result, err)
		c.Set(activitySuccessKey, err == nil && !result.Replayed)
		if err == nil {
			notifyClientsChanged()
		}
	})
}
