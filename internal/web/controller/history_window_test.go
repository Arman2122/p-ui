package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Arman2122/p-ui/internal/web/service"
)

func TestHistoryWindowFifteenMinutesAndLegacyTwoMinutes(t *testing.T) {
	mode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(mode) })
	engine := gin.New()
	(&ServerController{}).initRouter(engine.Group("/panel/api/server"))
	for _, bucket := range []int{15, 2} {
		t.Run(strconv.Itoa(bucket), func(t *testing.T) {
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/panel/api/server/historyWindow/"+strconv.Itoa(bucket), nil))
			var body struct {
				Success bool                        `json:"success"`
				Obj     service.MetricHistoryWindow `json:"obj"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if response.Code != http.StatusOK || !body.Success || body.Obj.Bucket != bucket {
				t.Fatalf("GET historyWindow/%d: status=%d body=%s", bucket, response.Code, response.Body.String())
			}
			if body.Obj.End <= 0 || body.Obj.End%int64(bucket) != 0 {
				t.Fatalf("unaligned end: %d", body.Obj.End)
			}
			if _, ok := body.Obj.Series["cpu"]; !ok {
				t.Fatalf("missing CPU series: %+v", body.Obj)
			}
		})
	}
}
