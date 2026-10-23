package controller

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/entity"
	"github.com/Arman2122/p-ui/internal/web/middleware"
	"github.com/Arman2122/p-ui/internal/web/service"
	"github.com/Arman2122/p-ui/internal/web/service/panel"
	"github.com/Arman2122/p-ui/internal/web/service/tgbot"
	"github.com/Arman2122/p-ui/internal/web/session"

	"github.com/gin-gonic/gin"
)

// APIController handles the main API routes for the Penhoon UI panel, including inbounds and server management.
type APIController struct {
	BaseController
	inboundController     *InboundController
	serverController      *ServerController
	nodeController        *NodeController
	hostController        *HostController
	egressController      *EgressController
	routingController     *RoutingController
	policyController      *PolicyController
	settingController     *SettingController
	xraySettingController *XraySettingController
	userService           panel.UserService
	Tgbot                 tgbot.Tgbot
}

// NewAPIController creates a new APIController instance and initializes its routes.
func NewAPIController(g *gin.RouterGroup) *APIController {
	a := &APIController{}
	a.initRouter(g)
	return a
}

func (a *APIController) checkAPIAuth(c *gin.Context) {
	// Node certificates authorize the dedicated control protocol, never a panel administrator.
	auth := c.GetHeader("Authorization")
	if after, ok := strings.CutPrefix(auth, "Bearer "); ok {
		tok := after
		if u, err := (&service.ResellerService{}).MatchToken(tok); err == nil {
			session.SetAPIAuthUser(c, u)
			c.Set("api_authed", true)
			c.Next()
			return
		}
		if u, scope, err := service.LegacyTokenUser(tok, c.ClientIP()); err == nil {
			if scope == model.ApiTokenScopeRead && !readOnlyMethod(c.Request.Method) {
				c.AbortWithStatusJSON(http.StatusForbidden, entity.Msg{Success: false, Msg: "this API token is read-only"})
				return
			}
			session.SetAPIAuthUser(c, u)
			c.Set("api_authed", true)
			c.Set("api_scope", scope)
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusUnauthorized, entity.Msg{Success: false, Msg: http.StatusText(http.StatusUnauthorized)})
		return
	}
	if !session.IsLogin(c) {
		if c.GetHeader("X-Requested-With") == "XMLHttpRequest" {
			c.AbortWithStatus(http.StatusUnauthorized)
		} else {
			c.AbortWithStatus(http.StatusNotFound)
		}
		return
	}
	if !session.IsAdmin(c) {
		if err := (&service.ResellerService{}).CheckAccess(session.GetLoginUser(c)); err != nil {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
	}
	c.Next()
}

// initRouter sets up the API routes for inbounds, server, and other endpoints.
func (a *APIController) initRouter(g *gin.RouterGroup) {
	// Main API group
	api := g.Group("/panel/api")
	api.Use(a.checkAPIAuth)
	// Decode + verify the node config envelope (zstd + X-Config-Sha256) and
	// advertise support, before CSRF/handlers read the body.
	api.Use(middleware.ConfigEnvelopeMiddleware())
	api.Use(middleware.CSRFMiddleware())
	api.Use(ActivityAuditMiddleware())
	NewActivityController(api)
	NewResellerController(api)
	NewResellerPlanController(api.Group("/reseller/plans"))
	api.GET("/openapi.json", ServeOpenAPISpec)
	api.Use(requireAdmin)
	NewBackupController(api)
	NewCoreConfigController(api)
	NewFleetController(api.Group("/fleet"))

	api.GET("/cores", a.getCores)
	api.GET("/cores/status", a.getCoresStatus)
	api.POST("/cores/:id/restart", a.restartCore)
	api.GET("/cores/:id/versions", a.getCoreVersions)
	api.POST("/cores/:id/install/:version", middleware.LongRequest(), a.installCoreVersion)
	api.GET("/cores/:id/logs", a.getCoreLogs)

	// Inbounds API
	inbounds := api.Group("/inbounds")
	a.inboundController = NewInboundController(inbounds)

	clients := api.Group("/clients")
	NewClientController(clients)
	NewGroupController(clients)

	// Server API
	server := api.Group("/server")
	a.serverController = NewServerController(server)

	// Nodes API — multi-panel management
	nodes := api.Group("/nodes")
	a.nodeController = NewNodeController(nodes)

	// Hosts API — per-inbound override endpoints for subscription links
	hosts := api.Group("/hosts")
	a.hostController = NewHostController(hosts)

	// Egresses API — policy-routed exits an L3 inbound can be sent out through
	egresses := api.Group("/egresses")
	a.egressController = NewEgressController(egresses)

	routingGroup := api.Group("/routing")
	a.routingController = NewRoutingController(routingGroup)

	// Policies API — the speed ladders clients are assigned to
	policies := api.Group("/policies")
	a.policyController = NewPolicyController(policies)

	// Settings + Xray config management live under the API surface too, so the
	// same API token drives them. Paths are /panel/api/setting/* and
	// /panel/api/xray/*.
	a.settingController = NewSettingController(api)
	a.xraySettingController = NewXraySettingController(api)

	// Extra routes
	api.POST("/backuptotgbot", middleware.LongRequest(), a.BackuptoTgbot)
}

// getCores describes what this build can serve, so the UI can render from what
// a core declares instead of from its own protocol switch.
func (a *APIController) getCores(c *gin.Context) {
	jsonObj(c, service.CoreViews(c.Request.Context()), nil)
}

/*
restartCore stops one core's daemons and converges them back.

Asked of the registry by id, so the panel is not an Xray-only control surface:
until this, an mtg sidecar could not be restarted from the panel at all.
*/
func (a *APIController) restartCore(c *gin.Context) {
	id := c.Param("id")
	err := (&service.ServerService{}).RestartCore(c.Request.Context(), id)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.xray.restartError"), err)
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.xray.restartSuccess"), nil)
}

/*
BackuptoTgbot sends a backup of the panel data to Telegram bot admins. It
answered 200 with an empty body whatever happened, which the published spec does
not describe and no client can read as an outcome. F279.

Synchronous on purpose: the operator asked for a backup and the answer is
whether it was delivered. What it needed was room to finish -- a full pg_dump
plus one upload per admin does not fit the panel's 30s write deadline. F294.
*/
func (a *APIController) BackuptoTgbot(c *gin.Context) {
	if err := a.Tgbot.SendBackupToAdmins(); err != nil {
		jsonMsg(c, I18nWeb(c, "pages.settings.telegramBotBackupFailed"), err)
		return
	}
	jsonMsg(c, I18nWeb(c, "pages.settings.telegramBotBackupSent"), nil)
}

func readOnlyMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}

func (a *APIController) getCoresStatus(c *gin.Context) {
	jsonObj(c, service.CoreStatusViews(c.Request.Context()), nil)
}

func (a *APIController) getCoreVersions(c *gin.Context) {
	view, err := service.CoreVersions(c.Request.Context(), c.Param("id"))
	if err != nil {
		jsonMsg(c, I18nWeb(c, "getVersion"), err)
		return
	}
	jsonObj(c, view, nil)
}

// installCoreVersion lands the binary and stops there: the daemon keeps running
// the old one until the operator restarts it, which the response says.
func (a *APIController) installCoreVersion(c *gin.Context) {
	err := service.InstallCoreVersion(c.Request.Context(), c.Param("id"), c.Param("version"))
	jsonMsg(c, I18nWeb(c, "pages.cores.installed"), err)
}

func (a *APIController) getCoreLogs(c *gin.Context) {
	instance, _ := strconv.Atoi(c.DefaultQuery("instance", "0"))
	n, _ := strconv.Atoi(c.DefaultQuery("n", "200"))
	if n <= 0 || n > 1000 {
		n = 200
	}
	lines, err := service.CoreLogs(c.Request.Context(), c.Param("id"), instance, n)
	if err != nil {
		jsonMsg(c, I18nWeb(c, "pages.cores.logsUnavailable"), err)
		return
	}
	jsonObj(c, lines, nil)
}
