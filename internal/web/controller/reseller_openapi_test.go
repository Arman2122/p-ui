package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/robfig/cron/v3"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/web/global"
)

type resellerOpenAPITestServer struct {
	ctx       context.Context
	scheduler *cron.Cron
}

func (s resellerOpenAPITestServer) GetCtx() context.Context { return s.ctx }
func (s resellerOpenAPITestServer) GetCron() *cron.Cron     { return s.scheduler }
func (s resellerOpenAPITestServer) GetWSHub() any           { return nil }

const resellerOpenAPIFixture = `{
  "openapi":"3.0.3","info":{"title":"Panel API","version":"test"},
  "servers":[{"url":"/"}],"x-websocket-messages":[{"type":"status"}],
  "components":{"schemas":{"Client":{"type":"object"}}},
  "paths":{
    "/panel/api/me":{"get":{"summary":"Identity"}},
    "/panel/api/activity":{"get":{"summary":"Own activity"}},
    "/panel/api/activity/read":{"post":{"summary":"Mark own read"}},
    "/panel/api/activity/alerts":{"get":{"summary":"Own alerts"}},
    "/panel/api/reseller/clients":{"get":{"summary":"Own clients"},"post":{"summary":"Create owned client"}},
    "/panel/api/reseller/clients/{id}/update":{"post":{"summary":"Edit owned client","responses":{"200":{"description":"OK","content":{"application/json":{"schema":{"$ref":"#/components/schemas/Client"}}}}}}},
    "/panel/api/reseller/tokens":{"get":{"summary":"Own tokens"}},
    "/panel/api/reseller/plans/list":{"get":{"summary":"Private plans"}},
    "/panel/api/clients/list":{"get":{"summary":"Admin clients"}},
    "/panel/api/setting/update":{"post":{"summary":"Admin settings"}},
    "/panel/api/resellers":{"get":{"summary":"Admin resellers"}},
    "/panel/api/reseller-admin":{"get":{"summary":"Similar prefix"}},
    "/panel/api/activity/admin":{"get":{"summary":"Wrong activity scope"}},
    "/login":{"post":{"summary":"Login"}},"/ws":{"get":{"summary":"Global events"}}
  }
}`

type resellerOpenAPIDoc struct {
	Paths      map[string]json.RawMessage `json:"paths"`
	Components json.RawMessage            `json:"components"`
	Servers    []struct {
		URL string `json:"url"`
	} `json:"servers"`
	WebSocketMessages json.RawMessage `json:"x-websocket-messages"`
}

func readResellerOpenAPIDoc(t *testing.T, data []byte) resellerOpenAPIDoc {
	t.Helper()
	var doc resellerOpenAPIDoc
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("decode OpenAPI: %v", err)
	}
	return doc
}

func TestResellerOpenAPIFilter(t *testing.T) {
	original := readResellerOpenAPIDoc(t, []byte(resellerOpenAPIFixture))
	for _, allowPlans := range []bool{false, true} {
		data, err := withResellerOpenAPI([]byte(resellerOpenAPIFixture), allowPlans)
		if err != nil {
			t.Fatal(err)
		}
		doc := readResellerOpenAPIDoc(t, data)
		want := []string{"/panel/api/me", "/panel/api/activity", "/panel/api/activity/read", "/panel/api/activity/alerts", "/panel/api/reseller/clients", "/panel/api/reseller/clients/{id}/update", "/panel/api/reseller/tokens"}
		if allowPlans {
			want = append(want, "/panel/api/reseller/plans/list")
		}
		got := make([]string, 0, len(doc.Paths))
		for path, operation := range doc.Paths {
			got = append(got, path)
			var before, after any
			if err := json.Unmarshal(original.Paths[path], &before); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(operation, &after); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("operation changed at %s", path)
			}
		}
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Fatalf("filtered paths=%v want%v", got, want)
		}
		if len(doc.WebSocketMessages) != 0 {
			t.Fatal("global websocket operations advertised")
		}
		if string(doc.Components) != `{"schemas":{"Client":{"type":"object"}}}` {
			t.Fatalf("schema references lost: %s", doc.Components)
		}
	}
	if _, err := withResellerOpenAPI([]byte(`{"paths":null}`), true); err == nil || err.Error() != "OpenAPI paths must be an object" {
		t.Fatalf("missing paths error=%v", err)
	}
}

func testResellerOpenAPIAuthorization(t *testing.T, userID int, token string, jar *http.Cookie) {
	t.Helper()
	previousFS := distFS
	previousMode := gin.Mode()
	previousServer := global.GetWebServer()
	t.Cleanup(func() { distFS = previousFS; gin.SetMode(previousMode); global.SetWebServer(previousServer) })
	global.SetWebServer(resellerOpenAPITestServer{ctx: t.Context(), scheduler: cron.New()})
	distFS = fstest.MapFS{"dist/openapi.json": &fstest.MapFile{Data: []byte(resellerOpenAPIFixture)}}
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(sessions.Sessions("p-ui", cookie.NewStore([]byte("0123456789abcdef0123456789abcdef"))))
	engine.Use(func(c *gin.Context) { c.Set("base_path", "/private-prefix/"); c.Next() })
	NewAPIController(engine.Group(""))
	call := func(path, bearer string, authenticatedCookie bool) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("X-Requested-With", "XMLHttpRequest")
		if authenticatedCookie {
			r.AddCookie(jar)
		}
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, r)
		return w
	}
	for _, bearer := range []string{"", token} {
		w := call("/panel/api/openapi.json", bearer, bearer == "")
		if w.Code != http.StatusOK {
			t.Fatalf("reseller schema status=%d body=%s", w.Code, w.Body.String())
		}
		doc := readResellerOpenAPIDoc(t, w.Body.Bytes())
		if len(doc.Paths) != 7 || doc.Paths["/panel/api/clients/list"] != nil || doc.Paths["/panel/api/reseller/plans/list"] != nil {
			t.Fatalf("reseller paths=%v", doc.Paths)
		}
		if len(doc.Servers) != 1 || doc.Servers[0].URL != "/private-prefix" {
			t.Fatalf("server prefix=%v", doc.Servers)
		}
		if w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatalf("unsafe schema caching: %s", w.Header().Get("Cache-Control"))
		}
		if forbidden := call("/panel/api/clients/list", bearer, bearer == ""); forbidden.Code != http.StatusForbidden {
			t.Fatalf("schema exemption widened actual API: %d", forbidden.Code)
		}
	}
	if w := call("/panel/api/openapi.json", "", false); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous schema status=%d", w.Code)
	}
	admin := call("/panel/api/openapi.json", "legacy-auth", false)
	if admin.Code != http.StatusOK {
		t.Fatalf("admin schema status=%d", admin.Code)
	}
	if doc := readResellerOpenAPIDoc(t, admin.Body.Bytes()); len(doc.Paths) != 15 || doc.Paths["/panel/api/clients/list"] == nil {
		t.Fatalf("admin schema was filtered: %v", doc.Paths)
	}
	db := database.GetDB()
	if err := db.Model(&model.ResellerAccount{}).Where("user_id = ?", userID).Update("allow_plans", true).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Model(&model.ResellerAccount{}).Where("user_id = ?", userID).Update("allow_plans", false).Error
	})
	if doc := readResellerOpenAPIDoc(t, call("/panel/api/openapi.json", token, false).Body.Bytes()); doc.Paths["/panel/api/reseller/plans/list"] == nil {
		t.Fatal("permitted private plans missing")
	}
	if err := db.Model(&model.ResellerAccount{}).Where("user_id = ?", userID).Update("expires_at", time.Now().Add(-time.Minute).UnixMilli()).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = db.Model(&model.ResellerAccount{}).Where("user_id = ?", userID).Update("expires_at", time.Now().Add(time.Hour).UnixMilli()).Error
	})
	if w := call("/panel/api/openapi.json", "", true); w.Code != http.StatusForbidden {
		t.Fatalf("expired account schema status=%d", w.Code)
	}
	if w := call("/panel/api/openapi.json", token, false); w.Code != http.StatusUnauthorized {
		t.Fatalf("expired token owner schema status=%d", w.Code)
	}
}
