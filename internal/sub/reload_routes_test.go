package sub

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

/*
Which routes the subscription server has was decided once, at boot.

subJsonEnable and subClashEnable gate whether those groups are registered at
all, and subPath / subJsonPath / subClashPath decide where. So enabling Clash
subscriptions, or moving the path, changed nothing an app could fetch until
someone restarted the panel -- while the panel started handing out the new URL
the moment the setting was saved. Every link an operator copied in that window
answered 404, which reads as a broken subscription, not as a pending restart.

Routed through Server.ServeHTTP rather than the engine, because the swap is what
is under test: the listener is untouched and the router behind it is replaced.
F253.
*/
func TestClashRoutesAppearWithoutARestart(t *testing.T) {
	initSubDB(t)
	SetDistFS(testDistFS)

	// A real subscription, so a 404 can only mean the route is absent.
	seedSubInbound(t, "reloadsub", "reload-vless", 45311, 0, `{"network":"tcp","security":"none"}`)

	server := NewServer()
	engine, err := server.initRouter()
	if err != nil {
		t.Fatalf("initRouter: %v", err)
	}
	server.handler.Store(engine)
	// Stands in for a bound listener; ReloadRoutes has nothing to do without one.
	server.httpServer = &http.Server{Handler: server}

	ask := func() int {
		rec := httptest.NewRecorder()
		server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/clash/reloadsub", nil))
		return rec.Code
	}
	if code := ask(); code != http.StatusNotFound {
		t.Fatalf("the clash path answered %d with subClashEnable off; this test proves nothing", code)
	}

	db := database.GetDB()
	if err := db.Save(&model.Setting{Key: "subClashEnable", Value: "true"}).Error; err != nil {
		t.Fatalf("enable clash subscriptions: %v", err)
	}
	if err := server.ReloadRoutes(); err != nil {
		t.Fatalf("ReloadRoutes: %v", err)
	}

	if code := ask(); code != http.StatusOK {
		t.Fatalf("the clash path answered %d after the setting was saved: every URL the panel is now handing out answers 404 until someone restarts the panel", code)
	}
}

// A rebuild that cannot read its settings must leave the router that is
// serving in place: a 404 for every client is worse than a stale path.
func TestAFailedReloadKeepsTheRouterThatIsServing(t *testing.T) {
	initSubDB(t)
	SetDistFS(testDistFS)

	server := NewServer()
	engine, err := server.initRouter()
	if err != nil {
		t.Fatalf("initRouter: %v", err)
	}
	server.handler.Store(engine)
	server.httpServer = &http.Server{Handler: server}

	if err := database.CloseDB(); err != nil {
		t.Fatalf("close the database: %v", err)
	}
	if err := server.ReloadRoutes(); err == nil {
		t.Fatal("a reload with no database reported success")
	}

	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sub/whoever", nil))
	if rec.Code == http.StatusServiceUnavailable && rec.Body.Len() == 0 {
		t.Fatal("the failed reload took the router with it")
	}
	if server.handler.Load() != engine {
		t.Fatal("a router that could not be built replaced the one that was serving")
	}
}
