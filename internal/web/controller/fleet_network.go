package controller

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/web/middleware"
	"github.com/Arman2122/p-ui/internal/web/service"
	"github.com/Arman2122/p-ui/internal/web/session"
)

func fleetNetworkService(c *gin.Context) (*service.FleetNetworkService, bool) {
	c.Header("Cache-Control", "no-store")
	network, err := service.NewFleetNetworkService(database.GetDB())
	if err != nil {
		jsonObj(c, nil, err)
		return nil, false
	}
	return network, true
}

func NewFleetNetworkController(parent *gin.RouterGroup) {
	g := parent.Group("/network", requireConfigAdmin)
	g.POST("/preview", func(c *gin.Context) {
		network, ok := fleetNetworkService(c)
		if !ok {
			return
		}
		request, ok := middleware.BindAndValidate[service.FleetNetworkPreviewRequest](c)
		if !ok {
			return
		}
		result, err := network.Preview(c.Request.Context(), session.GetLoginUser(c), *request)
		jsonObj(c, result, err)
	})
	g.POST("/publish", func(c *gin.Context) {
		network, ok := fleetNetworkService(c)
		if !ok {
			return
		}
		request, ok := middleware.BindAndValidate[service.FleetNetworkPublishRequest](c)
		if !ok {
			return
		}
		result, err := network.Publish(c.Request.Context(), session.GetLoginUser(c), *request)
		jsonObj(c, result, err)
	})
	g.GET("/defaults", func(c *gin.Context) {
		network, ok := fleetNetworkService(c)
		if !ok {
			return
		}
		result, err := network.ReadDefaults(c.Request.Context(), session.GetLoginUser(c))
		jsonObj(c, result, err)
	})
	g.POST("/defaults", func(c *gin.Context) {
		network, ok := fleetNetworkService(c)
		if !ok {
			return
		}
		request, ok := middleware.BindAndValidate[service.FleetNetworkDefaultsRequest](c)
		if !ok {
			return
		}
		result, err := network.ApproveDefaults(c.Request.Context(), session.GetLoginUser(c), request.ExpectedRevision, request.Specification)
		jsonObj(c, result, err)
	})
	g.GET("/nodes/:id", func(c *gin.Context) {
		network, ok := fleetNetworkService(c)
		if !ok {
			return
		}
		result, err := network.ReadNode(c.Request.Context(), session.GetLoginUser(c), c.Param("id"))
		jsonObj(c, result, err)
	})
	g.POST("/nodes/:id", func(c *gin.Context) {
		network, ok := fleetNetworkService(c)
		if !ok {
			return
		}
		request, ok := middleware.BindAndValidate[service.FleetNodeNetworkRequest](c)
		if !ok {
			return
		}
		if request.NodeID != c.Param("id") {
			jsonObj(c, nil, service.ErrFleetNetwork)
			return
		}
		result, err := network.ConfigureNode(c.Request.Context(), session.GetLoginUser(c), *request)
		jsonObj(c, result, err)
	})
	g.GET("/nodes/:id/assets", func(c *gin.Context) {
		network, ok := fleetNetworkService(c)
		if !ok {
			return
		}
		limit, err := strconv.Atoi(c.DefaultQuery("limit", "100"))
		if err != nil {
			jsonObj(c, nil, service.ErrFleetNetwork)
			return
		}
		result, err := network.ListAssets(c.Request.Context(), session.GetLoginUser(c), c.Param("id"), c.Query("after"), limit)
		jsonObj(c, result, err)
	})
	g.POST("/nodes/:id/assets", func(c *gin.Context) {
		network, ok := fleetNetworkService(c)
		if !ok {
			return
		}
		request, ok := middleware.BindAndValidate[service.FleetNetworkAssetRequest](c)
		if !ok {
			return
		}
		if request.NodeID != c.Param("id") {
			jsonObj(c, nil, service.ErrFleetNetwork)
			return
		}
		result, err := network.PutAsset(c.Request.Context(), session.GetLoginUser(c), *request)
		jsonObj(c, result, err)
	})
}
