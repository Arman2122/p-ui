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
	"github.com/Arman2122/p-ui/internal/web/entity"
	webruntime "github.com/Arman2122/p-ui/internal/web/runtime"
	"github.com/Arman2122/p-ui/internal/web/service"
	"github.com/Arman2122/p-ui/internal/web/session"
)

func TestFleetCreateRoutesRequireConfigurationAdministrator(t *testing.T) {
	for _, actor := range []struct{ role, scope string }{{"reseller", ""}, {"admin", model.ApiTokenScopeRead}} {
		router := gin.New()
		router.Use(func(c *gin.Context) {
			session.SetAPIAuthUser(c, &model.User{Id: 1, Role: actor.role})
			c.Set("api_scope", actor.scope)
			c.Next()
		})
		NewFleetClientCreateController(router.Group("/panel/api/fleet"))
		for _, request := range []struct{ method, path string }{{http.MethodPost, ""}, {http.MethodGet, "/capabilities"}, {http.MethodGet, "/creation/" + uuid.NewString()}} {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(request.method, "/panel/api/fleet/clients"+request.path, strings.NewReader(`{}`)))
			if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "administrator access is required") {
				t.Fatalf("unauthorized create operation HTTP%d %s", response.Code, response.Body.String())
			}
		}
	}
}

func TestFleetCreateRouteDispatchesAtomicOperationAndPrivateReceiptPostgres(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()
	var admin model.User
	if err := db.First(&admin).Error; err != nil {
		t.Fatal(err)
	}
	previous := webruntime.GetManager()
	t.Cleanup(func() { webruntime.SetManager(previous) })
	webruntime.SetManager(nil)
	actor := admin
	router := gin.New()
	router.Use(func(c *gin.Context) { session.SetAPIAuthUser(c, &actor); c.Next() }, ActivityAuditMiddleware())
	NewFleetClientCreateController(router.Group("/panel/api/fleet"))
	request := entity.ClientCreatePayload{OperationID: uuid.NewString(), ExpectedActorID: admin.Id, Client: model.Client{Email: "api-owned-source", Enable: false, Password: "never-echo-this-credential"}, InboundIds: []int{}}
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(method, "/panel/api/fleet/clients"+path, bytes.NewReader(encoded))
		r.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, r)
		if response.Header().Get("Cache-Control") != "no-store" || strings.Contains(response.Body.String(), request.Client.Password) {
			t.Fatal("source API cached or exposed credentials")
		}
		return response
	}
	read := func(response *httptest.ResponseRecorder) (bool, entity.FleetClientCreateResult) {
		t.Helper()
		var result struct {
			Success bool                           `json:"success"`
			Obj     entity.FleetClientCreateResult `json:"obj"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		return result.Success, result.Obj
	}
	if success, _ := read(call(http.MethodPost, "", request)); success {
		t.Fatal("source create bypassed unavailable runtime")
	}
	capability := call(http.MethodGet, "/capabilities", nil)
	if !strings.Contains(capability.Body.String(), `"supported":false`) {
		t.Fatal("absent runtime advertised source creation")
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
	if !strings.Contains(call(http.MethodGet, "/capabilities", nil).Body.String(), `"supported":true`) {
		t.Fatal("installed runtime did not advertise source creation")
	}
	actor.LoginEpoch++
	if success, _ := read(call(http.MethodPost, "", request)); success {
		t.Fatal("stale administrator session created source")
	}
	actor = admin
	success, created := read(call(http.MethodPost, "", request))
	if !success || created.ActorID != admin.Id || created.PanelClientID <= 0 || created.Revision != 1 || !created.Pending {
		t.Fatalf("create returned %+v success=%v", created, success)
	}
	if success, replay := read(call(http.MethodPost, "", request)); !success || replay.ClientID != created.ClientID {
		t.Fatalf("lost response retry changed source=%+v success=%v", replay, success)
	}
	if success, receipt := read(call(http.MethodGet, "/creation/"+request.OperationID, nil)); !success || receipt.ClientID != created.ClientID {
		t.Fatalf("creation receipt was not recoverable: %+v success=%v", receipt, success)
	}
	var count int64
	if err := db.Model(&model.ActivityEvent{}).Where("kind = ?", "audit.fleet.create").Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("creation audit included rejected/replayed operations=%d err=%v", count, err)
	}
	other := admin
	other.Id, other.Username = 0, "other-admin"
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}
	actor = other
	creationOperationID := request.OperationID
	request.OperationID = uuid.NewString()
	request.Client.Email = "changed-session-must-not-create"
	if success, _ := read(call(http.MethodPost, "", request)); success {
		t.Fatal("changed authenticated actor adopted a cached actor's request")
	}
	request.ExpectedActorID = 0
	if success, _ := read(call(http.MethodPost, "", request)); success {
		t.Fatal("owned API accepted a request without its actor precondition")
	}
	if success, _ := read(call(http.MethodGet, "/creation/"+creationOperationID, nil)); success {
		t.Fatal("another actor read a private creation receipt")
	}
	actor = admin
	if success, _ := read(call(http.MethodGet, "/creation/"+uuid.NewString(), nil)); success {
		t.Fatal("missing receipt was represented as a confirmed outcome")
	}
	if err := db.Model(&model.ClientRecord{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("retry or rejected request duplicated source=%d err=%v", count, err)
	}
}
