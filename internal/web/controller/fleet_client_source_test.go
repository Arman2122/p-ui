package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/entity"
	"github.com/Arman2122/p-ui/internal/web/service"
	"github.com/Arman2122/p-ui/internal/web/session"
)

func TestFleetSourceReadRouteProtectsCoherentPrivateSourcePostgres(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()
	var admin model.User
	if err := db.First(&admin).Error; err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateFleetClientTx(t.Context(), db, &admin, entity.ClientCreatePayload{
		OperationID: uuid.NewString(), Client: model.Client{Email: "source-read-api", Password: "private-common-value", Enable: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	actor, scope := admin, ""
	router := gin.New()
	router.Use(func(c *gin.Context) { session.SetAPIAuthUser(c, &actor); c.Set("api_scope", scope); c.Next() })
	NewFleetClientSourceController(router.Group("/panel/api/fleet"))
	read := func() *httptest.ResponseRecorder {
		t.Helper()
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/panel/api/fleet/clients/"+created.ClientID+"/source", nil))
		return response
	}
	response := read()
	var body struct {
		Success bool                         `json:"success"`
		Obj     entity.FleetClientSourceView `json:"obj"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || !body.Success || body.Obj.ActorID != admin.Id || body.Obj.ClientID != created.ClientID || body.Obj.Revision != 1 || body.Obj.Client.Password != "private-common-value" || !body.Obj.EffectiveScopeKnown || body.Obj.EffectiveInboundIDs == nil || len(*body.Obj.EffectiveInboundIDs) != 0 {
		t.Fatalf("private source read lost coherent state: success=%v err=%v", body.Success, err)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("private source response is cacheable")
	}
	scope = model.ApiTokenScopeRead
	if response := read(); response.Code != http.StatusForbidden {
		t.Fatalf("read-only token obtained source credentials: HTTP%d", response.Code)
	}
	scope, actor.Role = "", "reseller"
	if response := read(); response.Code != http.StatusForbidden {
		t.Fatalf("reseller obtained source credentials: HTTP%d", response.Code)
	}
	actor = admin
	actor.LoginEpoch++
	response = read()
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body.Success {
		t.Fatalf("stale administrator read source credentials: success=%v err=%v", body.Success, err)
	}
}
