package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

/*
The panel's write deadline is 30s for every route, and three admin handlers
routinely outrun it: installXray downloads under a 60s client, updateGeofile
fetches six files under a five-minute one, and a REALITY scan can queue 512
probes. Their responses were cut mid-write -- the operator saw a dead
connection while the work ran to completion, and retried, starting a second one.

100ms against a 300ms handler here for what is 30s against minutes there. F284.
*/
func TestLongRequestOutlastsTheServerWriteDeadline(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	slow := func(c *gin.Context) {
		time.Sleep(300 * time.Millisecond)
		c.String(http.StatusOK, "done")
	}
	engine.GET("/bare", slow)
	engine.GET("/long", LongRequest(), slow)

	server := httptest.NewUnstartedServer(engine)
	server.Config.WriteTimeout = 100 * time.Millisecond
	server.Start()
	t.Cleanup(server.Close)

	client := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
	ask := func(path string) error {
		resp, err := client.Get(server.URL + path)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		return nil
	}

	if err := ask("/bare"); err == nil {
		t.Fatal("a handler slower than the server's write deadline answered anyway; this test proves nothing about lifting it")
	}
	if err := ask("/long"); err != nil {
		t.Fatalf("the route asked for room still had its response cut at the server deadline: %v", err)
	}
}
