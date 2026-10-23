package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/session"
)

func TestFleetNetworkRejectsResellerAndReadTokens(t *testing.T) {
	for _, test := range []struct{ name, role, scope string }{{"reseller", "reseller", ""}, {"read token", "admin", model.ApiTokenScopeRead}} {
		t.Run(test.name, func(t *testing.T) {
			router := gin.New()
			router.Use(func(c *gin.Context) {
				session.SetAPIAuthUser(c, &model.User{Id: 1, Role: test.role})
				c.Set("api_scope", test.scope)
				c.Next()
			})
			NewFleetNetworkController(router.Group("/panel/api/fleet"))
			for _, request := range []struct{ method, path string }{{"GET", "/defaults"}, {"POST", "/defaults"}, {"POST", "/preview"}, {"POST", "/publish"}, {"GET", "/nodes/11111111-1111-4111-8111-111111111111"}, {"POST", "/nodes/11111111-1111-4111-8111-111111111111"}, {"GET", "/nodes/11111111-1111-4111-8111-111111111111/assets"}, {"POST", "/nodes/11111111-1111-4111-8111-111111111111/assets"}} {
				recorder := httptest.NewRecorder()
				router.ServeHTTP(recorder, httptest.NewRequest(request.method, "/panel/api/fleet/network"+request.path, strings.NewReader(`{}`)))
				if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "administrator access is required") {
					t.Fatalf("%s %s allowed configuration access: HTTP%d %s", request.method, request.path, recorder.Code, recorder.Body.String())
				}
			}
		})
	}
}

func TestFleetNetworkRoutesPreserveRevisionFenceAndAuditPrivacy(t *testing.T) {
	initTestDB(t)
	var user model.User
	if err := database.GetDB().First(&user).Error; err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(func(c *gin.Context) { session.SetAPIAuthUser(c, &user); c.Next() }, ActivityAuditMiddleware())
	NewFleetNetworkController(router.Group("/panel/api/fleet"))
	body := `{"expectedRevision":0,"specification":{"version":1,"bases":{"xray":{"core":"xray","configuration":{"outbounds":[{"protocol":"socks","tag":"proxy","settings":{"servers":[{"address":"proxy.example.com","port":1080,"users":[{"user":"private-user","pass":"private-password"}]}]}}]}}}}}`
	post := func(want bool) {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/panel/api/fleet/network/defaults", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		var result struct {
			Success bool   `json:"success"`
			Msg     string `json:"msg"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Success != want || recorder.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("network write success=%v message=%q HTTP%d", result.Success, result.Msg, recorder.Code)
		}
		if strings.Contains(recorder.Body.String(), "private-password") {
			t.Fatal("mutation response exposed configuration secret")
		}
	}
	post(true)
	body = strings.Replace(body, "proxy.example.com", "changed.example.com", 1)
	post(false)
	var rows []model.ActivityEvent
	if err := database.GetDB().Where("kind = ?", "audit.fleet.update").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ActorUserID != user.Id || strings.Contains(rows[0].Params, "private-") {
		t.Fatalf("unsafe or missing configuration audit: count=%d", len(rows))
	}
}
