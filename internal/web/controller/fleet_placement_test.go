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

func TestFleetPlacementRoutesRejectUnauthorizedPolicyChanges(t *testing.T) {
	for _, test := range []struct{ role, scope string }{{"reseller", ""}, {"admin", model.ApiTokenScopeRead}} {
		t.Run(test.role+test.scope, func(t *testing.T) {
			router := gin.New()
			router.Use(func(c *gin.Context) {
				session.SetAPIAuthUser(c, &model.User{Id: 1, Role: test.role})
				c.Set("api_scope", test.scope)
				c.Next()
			})
			NewFleetPlacementController(router.Group("/panel/api/fleet"))
			for _, path := range []string{"/default", "/rules", "/nodes/11111111-1111-4111-8111-111111111111", "/clients/11111111-1111-4111-8111-111111111111"} {
				for _, method := range []string{http.MethodGet, http.MethodPost} {
					recorder := httptest.NewRecorder()
					router.ServeHTTP(recorder, httptest.NewRequest(method, "/panel/api/fleet/placement"+path, strings.NewReader(`{}`)))
					if recorder.Code != http.StatusForbidden || !strings.Contains(recorder.Body.String(), "administrator access is required") {
						t.Fatalf("unauthorized placement route=%s %s HTTP%d %s", method, path, recorder.Code, recorder.Body.String())
					}
				}
			}
		})
	}
}

func TestFleetPlacementRouteRequiresRuntimeAndPreservesCASPostgres(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()
	var admin model.User
	if err := db.First(&admin).Error; err != nil {
		t.Fatal(err)
	}
	source := model.ClientRecord{Email: "placement-api", UUID: uuid.NewString(), Enable: true}
	if err := db.Create(&source).Error; err != nil {
		t.Fatal(err)
	}
	client := model.FleetClient{ID: uuid.NewString(), PanelClientID: &source.Id, Revision: 1, CredentialGeneration: 1, CredentialDigest: bytes.Repeat([]byte{1}, 32), Owned: true}
	if err := db.Create(&client).Error; err != nil {
		t.Fatal(err)
	}
	prior := webruntime.GetManager()
	t.Cleanup(func() { webruntime.SetManager(prior) })
	webruntime.SetManager(nil)
	router := gin.New()
	router.Use(func(c *gin.Context) { session.SetAPIAuthUser(c, &admin); c.Next() }, ActivityAuditMiddleware())
	NewFleetPlacementController(router.Group("/panel/api/fleet"))
	ids := []int{}
	request := webruntime.SourcePlacementRequest{OperationID: uuid.NewString(), ClientID: client.ID, ExpectedRevision: 1, Action: "restrict", InboundIDs: &ids}
	post := func() (bool, string, webruntime.SourcePlacementResult) {
		t.Helper()
		data, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		httpRequest := httptest.NewRequest(http.MethodPost, "/panel/api/fleet/placement/clients/"+client.ID, bytes.NewReader(data))
		httpRequest.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httpRequest)
		var response struct {
			Success bool                             `json:"success"`
			Msg     string                           `json:"msg"`
			Obj     webruntime.SourcePlacementResult `json:"obj"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if recorder.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("placement mutation response is cacheable")
		}
		return response.Success, response.Msg, response.Obj
	}
	if success, message, _ := post(); success || !strings.Contains(message, webruntime.ErrFleetDispatcher.Error()) {
		t.Fatalf("placement route bypassed runtime: success=%v message=%q", success, message)
	}
	dispatcher, err := service.NewFleetRuntime(db)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := webruntime.NewFleetManager(dispatcher)
	if err != nil {
		t.Fatal(err)
	}
	webruntime.SetManager(manager)
	if success, message, result := post(); !success || !result.Pending || result.Revision != 2 {
		t.Fatalf("authorized placement dispatch failed: success=%v message=%q result=%+v", success, message, result)
	}
	request.OperationID, request.InboundIDs = uuid.NewString(), nil
	if success, message, _ := post(); success || !strings.Contains(message, "revision") {
		t.Fatalf("stale placement CAS overwrote restriction: success=%v message=%q", success, message)
	}
	var current model.FleetClient
	if err := db.First(&current, "id = ?", client.ID).Error; err != nil || current.PlacementRevision != 2 {
		t.Fatalf("route failure changed source: %+v %v", current, err)
	}
	var events int64
	if err := db.Model(&model.ActivityEvent{}).Where("kind = 'audit.fleet.update'").Count(&events).Error; err != nil || events != 1 {
		t.Fatalf("placement mutation audit=%d err=%v", events, err)
	}
}
