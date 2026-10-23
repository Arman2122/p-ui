package controller

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/Arman2122/p-ui/internal/web/middleware"
	"github.com/Arman2122/p-ui/internal/web/service"
)

func NewFleetController(g *gin.RouterGroup) {
	NewFleetNetworkController(g)
	NewFleetPlacementController(g)
	NewFleetResetController(g)
	NewFleetClientCreateController(g)
	NewFleetClientSourceController(g)
	g.GET("/status", func(c *gin.Context) { status, err := service.FleetStatus(c.Request.Context()); jsonObj(c, status, err) })
	g.GET("/nodes", func(c *gin.Context) {
		limit, err := strconv.Atoi(c.DefaultQuery("limit", "200"))
		if err != nil {
			jsonObj(c, nil, err)
			return
		}
		nodes, err := service.FleetNodes(c.Request.Context(), c.Query("after"), limit)
		jsonObj(c, nodes, err)
	})
	g.POST("/enroll", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		request, ok := middleware.BindAndValidate[service.FleetEnrollmentRequest](c)
		if !ok {
			return
		}
		result, err := service.EnrollFleetNode(c.Request.Context(), *request)
		jsonObj(c, result, err)
	})
}
