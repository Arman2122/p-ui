package controller

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/util/crypto"
	"github.com/Arman2122/p-ui/internal/web/service"
	"github.com/Arman2122/p-ui/internal/web/session"
)

func TestResellerAuthorizationPostgres(t *testing.T) {
	initTestDB(t)
	db := database.GetDB()
	var reseller model.User
	if err := db.Order("id").First(&reseller).Error; err != nil {
		t.Fatal(err)
	}
	reseller.Username, reseller.Role, reseller.Password = "reseller-auth", "reseller", "unused"
	if err := db.Save(&reseller).Error; err != nil {
		t.Fatal(err)
	}
	admin := model.User{Id: 200, Username: "admin-auth", Role: "admin", Password: "unused"}
	if err := db.Create(&admin).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ResellerAccount{UserID: reseller.Id, Name: "Partner", ExpiresAt: time.Now().Add(time.Hour).UnixMilli()}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.ApiToken{Name: "legacy-admin-auth", Token: crypto.HashTokenSHA256("legacy-auth"), Enabled: true, Scope: "admin"}).Error; err != nil {
		t.Fatal(err)
	}
	token, err := (&service.ResellerService{}).CreateToken(&reseller, "reseller-token", time.Now().Add(time.Hour).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	engine := gin.New()
	engine.Use(sessions.Sessions("p-ui", cookie.NewStore([]byte("0123456789abcdef0123456789abcdef"))))
	engine.GET("/login", func(c *gin.Context) {
		if err := session.SetLoginUser(c, &reseller); err != nil {
			t.Error(err)
		}
		c.Status(200)
	})
	engine.GET("/logout", (&IndexController{}).logout)
	api := engine.Group("/panel/api")
	a := &APIController{}
	api.Use(a.checkAPIAuth)
	NewResellerController(api)
	api.Use(requireAdmin)
	for _, path := range []string{"/clients/list", "/inbounds/list", "/setting/all", "/nodes", "/cores"} {
		api.GET(path, func(c *gin.Context) { c.String(200, "admin-secret") })
	}
	api.POST("/setting/update", func(c *gin.Context) { c.String(200, "mutated") })
	engine.GET("/ws", NewWebSocketController(nil).HandleWebSocket)
	mint := httptest.NewRecorder()
	engine.ServeHTTP(mint, httptest.NewRequest(http.MethodGet, "/login", nil))
	jar := mint.Result().Cookies()[0]
	call := func(method, path, bearer string, withCookie bool) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, path, nil)
		if withCookie {
			r.AddCookie(jar)
		}
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, r)
		return w
	}
	for _, bearer := range []string{"", token.Token} {
		for _, path := range []string{"/clients/list", "/inbounds/list", "/setting/all", "/nodes", "/cores"} {
			w := call("GET", "/panel/api"+path, bearer, true)
			if w.Code != 403 {
				t.Fatalf("%s status=%d body=%s", path, w.Code, w.Body.String())
			}
		}
		if w := call("POST", "/panel/api/setting/update", bearer, true); w.Code != 403 {
			t.Fatalf("mutation status=%d", w.Code)
		}
	}
	if w := call("GET", "/ws", "", true); w.Code != 403 {
		t.Fatalf("websocket status=%d want403", w.Code)
	}
	if w := call("GET", "/panel/api/me", "bad-token", true); w.Code != 401 {
		t.Fatalf("invalid bearer fell back to cookie: %d", w.Code)
	}
	for _, bearer := range []string{"legacy-auth", token.Token} {
		w := call("GET", "/panel/api/me", bearer, false)
		if w.Code != 200 {
			t.Fatalf("me status=%d %s", w.Code, w.Body.String())
		}
		var body struct {
			Obj service.SignedInUser `json:"obj"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		want := "admin"
		if bearer == token.Token {
			want = "reseller"
		}
		if body.Obj.Role != want {
			t.Fatalf("role=%s want%s", body.Obj.Role, want)
		}
	}
	t.Run("mtls never inherits first reseller", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/panel/api/me", nil)
		r.TLS = &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, r)
		var body struct {
			Obj service.SignedInUser `json:"obj"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.Obj.Role != "admin" || body.Obj.ID != admin.Id {
			t.Fatalf("mTLS role=%q user=%d want admin%d", body.Obj.Role, body.Obj.ID, admin.Id)
		}
	})
	t.Run("OpenAPI exposes only permitted operations", func(t *testing.T) { testResellerOpenAPIAuthorization(t, reseller.Id, token.Token, jar) })
	if w := call("GET", "/logout", "", true); w.Code != 200 {
		t.Fatalf("logout status=%d", w.Code)
	}
	var freshAdmin model.User
	if err := db.First(&freshAdmin, admin.Id).Error; err != nil {
		t.Fatal(err)
	}
	if freshAdmin.LoginEpoch != 0 {
		t.Fatalf("reseller logout changed admin epoch: %d", freshAdmin.LoginEpoch)
	}
	if w := call("GET", "/panel/api/me", token.Token, false); w.Code != 401 {
		t.Fatalf("inherited token survived logout: %d", w.Code)
	}
}
