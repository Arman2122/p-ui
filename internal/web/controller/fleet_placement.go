package controller

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Arman2122/p-ui/internal/controlplane"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/web/middleware"
	webruntime "github.com/Arman2122/p-ui/internal/web/runtime"
	"github.com/Arman2122/p-ui/internal/web/service"
	"github.com/Arman2122/p-ui/internal/web/session"
)

func fleetPlacementService(c *gin.Context) (*service.FleetPlacementService, bool) {
	c.Header("Cache-Control", "no-store")
	placement, err := service.NewFleetPlacementService(database.GetDB())
	if err != nil {
		jsonObj(c, nil, err)
		return nil, false
	}
	return placement, true
}

func NewFleetPlacementController(parent *gin.RouterGroup) {
	g := parent.Group("/placement", requireConfigAdmin)
	g.GET("/nodes/:id", func(c *gin.Context) {
		placement, ok := fleetPlacementService(c)
		if !ok {
			return
		}
		view, err := placement.ReadNode(c.Request.Context(), session.GetLoginUser(c), c.Param("id"))
		jsonObj(c, view, err)
	})
	g.POST("/nodes/:id", func(c *gin.Context) {
		placement, ok := fleetPlacementService(c)
		if !ok {
			return
		}
		request, ok := middleware.BindAndValidate[service.FleetNodePlacementRequest](c)
		if !ok {
			return
		}
		if request.NodeID != c.Param("id") {
			jsonObj(c, nil, controlplane.ErrScope)
			return
		}
		view, err := placement.ConfigureNode(c.Request.Context(), session.GetLoginUser(c), *request)
		jsonObj(c, view, err)
	})
	g.GET("/rules", func(c *gin.Context) {
		placement, ok := fleetPlacementService(c)
		if !ok {
			return
		}
		limit, err := strconv.Atoi(c.DefaultQuery("limit", "100"))
		if err != nil {
			jsonObj(c, nil, controlplane.ErrInvalid)
			return
		}
		view, err := placement.ListRules(c.Request.Context(), session.GetLoginUser(c), c.Query("after"), limit)
		jsonObj(c, view, err)
	})
	g.POST("/rules", func(c *gin.Context) {
		placement, ok := fleetPlacementService(c)
		if !ok {
			return
		}
		request, ok := middleware.BindAndValidate[service.FleetPlacementRuleRequest](c)
		if !ok {
			return
		}
		view, err := placement.PutRule(c.Request.Context(), session.GetLoginUser(c), *request)
		jsonObj(c, view, err)
	})
	g.GET("/default", func(c *gin.Context) {
		placement, ok := fleetPlacementService(c)
		if !ok {
			return
		}
		view, err := placement.ReadDefault(c.Request.Context(), session.GetLoginUser(c))
		jsonObj(c, view, err)
	})
	g.POST("/default", func(c *gin.Context) {
		placement, ok := fleetPlacementService(c)
		if !ok {
			return
		}
		request, ok := middleware.BindAndValidate[service.FleetPlacementDefaultRequest](c)
		if !ok {
			return
		}
		view, err := placement.ConfigureDefault(c.Request.Context(), session.GetLoginUser(c), *request)
		jsonObj(c, view, err)
	})
	g.GET("/clients/:id", func(c *gin.Context) {
		placement, ok := fleetPlacementService(c)
		if !ok {
			return
		}
		view, err := placement.PreviewClient(c.Request.Context(), session.GetLoginUser(c), c.Param("id"))
		jsonObj(c, view, err)
	})
	g.POST("/clients/:id", func(c *gin.Context) {
		placement, ok := fleetPlacementService(c)
		if !ok {
			return
		}
		request, ok := middleware.BindAndValidate[webruntime.SourcePlacementRequest](c)
		if !ok {
			return
		}
		if request.ClientID != c.Param("id") {
			jsonObj(c, nil, controlplane.ErrScope)
			return
		}
		view, err := placement.ConfigureClient(c.Request.Context(), session.GetLoginUser(c), *request)
		jsonObj(c, view, err)
	})
}
