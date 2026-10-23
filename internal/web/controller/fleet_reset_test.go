package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	webruntime "github.com/Arman2122/p-ui/internal/web/runtime"
	"github.com/Arman2122/p-ui/internal/web/service"
	"github.com/Arman2122/p-ui/internal/web/session"
)

func TestFleetResetRouteRejectsResellerAndReadOnlyTokens(t *testing.T) {
	for _, actor := range []struct{ role, scope string }{{"reseller", ""}, {"admin", model.ApiTokenScopeRead}} {
		router := gin.New()
		router.Use(func(c *gin.Context) {
			session.SetAPIAuthUser(c, &model.User{Id: 1, Role: actor.role})
			c.Set("api_scope", actor.scope)
			c.Next()
		})
		NewFleetResetController(router.Group("/panel/api/fleet"))
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/panel/api/fleet/clients/11111111-1111-4111-8111-111111111111/reset-schedule", strings.NewReader(`{}`)))
		if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "administrator access is required") {
			t.Fatalf("unauthorized reset mutation HTTP%d %s", response.Code, response.Body.String())
		}
	}
}

func TestFleetResetRouteDispatchesOwnedPolicyAndAuditsOnlyCommittedChangePostgres(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()
	var admin model.User
	if err := db.First(&admin).Error; err != nil {
		t.Fatal(err)
	}
	source := model.ClientRecord{Email: "reset-api", UUID: uuid.NewString(), Enable: true}
	if err := db.Create(&source).Error; err != nil {
		t.Fatal(err)
	}
	client := model.FleetClient{ID: uuid.NewString(), PanelClientID: &source.Id, Revision: 1, CredentialGeneration: 1, CredentialDigest: bytes.Repeat([]byte{1}, 32), Owned: true}
	if err := db.Create(&client).Error; err != nil {
		t.Fatal(err)
	}
	previous := webruntime.GetManager()
	t.Cleanup(func() { webruntime.SetManager(previous) })
	webruntime.SetManager(nil)
	router := gin.New()
	router.Use(func(c *gin.Context) { session.SetAPIAuthUser(c, &admin); c.Next() }, ActivityAuditMiddleware())
	NewFleetResetController(router.Group("/panel/api/fleet"))
	request := webruntime.ResetScheduleRequest{OperationID: uuid.NewString(), ClientID: client.ID, Override: true, Cycle: "monthly"}
	post := func() (bool, string, webruntime.ResetScheduleView) {
		t.Helper()
		data, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		httpRequest := httptest.NewRequest(http.MethodPost, "/panel/api/fleet/clients/"+client.ID+"/reset-schedule", bytes.NewReader(data))
		httpRequest.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httpRequest)
		var body struct {
			Success bool                         `json:"success"`
			Msg     string                       `json:"msg"`
			Obj     webruntime.ResetScheduleView `json:"obj"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if response.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("reset policy response is cacheable")
		}
		return body.Success, body.Msg, body.Obj
	}
	if success, message, _ := post(); success || !strings.Contains(message, webruntime.ErrFleetDispatcher.Error()) {
		t.Fatalf("reset route bypassed owned runtime=%v %q", success, message)
	}
	dispatch, err := service.NewFleetRuntime(db)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := webruntime.NewFleetManager(dispatch)
	if err != nil {
		t.Fatal(err)
	}
	webruntime.SetManager(manager)
	request.ClientID = uuid.NewString()
	if success, message, _ := post(); success || !strings.Contains(message, "scope") {
		t.Fatalf("body client overrode route client=%v %q", success, message)
	}
	request.ClientID = client.ID
	if success, message, result := post(); !success || result.Revision != 1 || result.Cycle != "monthly" || result.NextDueUnixMilli != 0 {
		t.Fatalf("owned reset policy=%v %q %+v", success, message, result)
	}
	request.OperationID, request.Cycle = uuid.NewString(), "daily"
	if success, message, _ := post(); success || !strings.Contains(message, "revision") {
		t.Fatalf("stale reset policy overwrote current cadence=%v %q", success, message)
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/panel/api/fleet/clients/"+client.ID+"/reset-schedule", nil))
	var body struct {
		Success bool                         `json:"success"`
		Obj     webruntime.ResetScheduleView `json:"obj"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || !body.Success || body.Obj.Cycle != "monthly" || body.Obj.Revision != 1 {
		t.Fatalf("reset read lost committed policy=%+v err=%v", body, err)
	}
	var events int64
	if err := db.Model(&model.ActivityEvent{}).Where("kind = 'audit.fleet.update'").Count(&events).Error; err != nil || events != 1 {
		t.Fatalf("reset audit includes uncommitted changes=%d err=%v", events, err)
	}
}
