package controller

import (
	"github.com/gin-gonic/gin"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/web/middleware"
	webruntime "github.com/Arman2122/p-ui/internal/web/runtime"
	"github.com/Arman2122/p-ui/internal/web/service"
	"github.com/Arman2122/p-ui/internal/web/session"
)

func NewFleetResetController(parent *gin.RouterGroup) {
	g := parent.Group("/clients", requireConfigAdmin)
	g.GET("/:id/reset-schedule", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		view, err := service.ReadFleetResetSchedule(c.Request.Context(), session.GetLoginUser(c), c.Param("id"))
		jsonObj(c, view, err)
	})
	g.POST("/:id/reset-schedule", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		request, ok := middleware.BindAndValidate[webruntime.ResetScheduleRequest](c)
		if !ok {
			return
		}
		if request.ClientID != c.Param("id") {
			jsonObj(c, nil, controlplane.ErrScope)
			return
		}
		view, err := service.ConfigureFleetResetSchedule(c.Request.Context(), session.GetLoginUser(c), *request)
		jsonObj(c, view, err)
	})
	g.POST("/:id/reset-schedule/cancel", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		request, ok := middleware.BindAndValidate[webruntime.ResetScheduleCancelRequest](c)
		if !ok {
			return
		}
		if request.ClientID != c.Param("id") {
			jsonObj(c, nil, controlplane.ErrScope)
			return
		}
		view, err := service.CancelFleetResetSchedule(c.Request.Context(), session.GetLoginUser(c), *request)
		jsonObj(c, view, err)
	})
}
