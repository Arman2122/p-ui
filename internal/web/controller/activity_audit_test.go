package controller

import (
	"errors"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/session"
)

func TestActivityAuditRecordsSuccessfulMutationWithoutRequestSecrets(t *testing.T) {
	initTestDB(t)
	var user model.User
	if err := database.GetDB().First(&user).Error; err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	router.Use(func(c *gin.Context) { session.SetAPIAuthUser(c, &user); c.Next() }, ActivityAuditMiddleware())
	router.POST("/panel/api/clients/add", func(c *gin.Context) { jsonObj(c, map[string]string{"password": "also-private"}, nil) })
	router.POST("/panel/api/clients/del/:id", func(c *gin.Context) { jsonMsg(c, "", errors.New("refused")) })
	router.GET("/panel/api/clients/list", func(c *gin.Context) { jsonObj(c, nil, nil) })
	for _, path := range []string{"/panel/api/resellers", "/panel/api/reseller/clients", "/panel/api/resellers/:id/top-up", "/panel/api/backups/schedule"} {
		router.POST(path, func(c *gin.Context) { jsonObj(c, nil, nil) })
	}
	for _, tc := range []struct{ method, path string }{
		{"POST", "/panel/api/clients/add"},
		{"POST", "/panel/api/clients/del/7"},
		{"GET", "/panel/api/clients/list"},
		{"POST", "/panel/api/resellers"},
		{"POST", "/panel/api/reseller/clients"},
		{"POST", "/panel/api/resellers/7/top-up"},
		{"POST", "/panel/api/backups/schedule"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"password":"private-request"}`))
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, req)
	}
	var rows []model.ActivityEvent
	if err := database.GetDB().Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	want := []string{"audit.clients.add", "audit.resellers.create", "audit.reseller.create", "audit.resellers.topup", "audit.backups.update"}
	var got []string
	for _, row := range rows {
		got = append(got, row.Kind)
		if row.ActorUserID != user.Id || strings.Contains(row.Params, "private") || strings.Contains(row.Params, "password") {
			t.Fatal("audit lost actor attribution or retained a request/response secret")
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("audit rows: %+v", rows)
	}
}
