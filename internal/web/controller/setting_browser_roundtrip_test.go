package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func postSettings(t *testing.T, router http.Handler, path, body string) json.RawMessage {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("POST %s: status %d, body %s", path, resp.Code, resp.Body.String())
	}
	var answer struct {
		Success bool            `json:"success"`
		Msg     string          `json:"msg"`
		Obj     json.RawMessage `json:"obj"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &answer); err != nil {
		t.Fatalf("POST %s: not a panel answer: %v (%s)", path, err, resp.Body.String())
	}
	if !answer.Success {
		t.Fatalf("POST %s: success=false, msg %q", path, answer.Msg)
	}
	return answer.Obj
}

/*
The settings tests start from GetAllSetting in Go; nothing posted what the
browser posts, which is the whole settings view straight back to /update.
A field the view emits but the form cannot bind, or a value the validator
refuses on its own default, breaks every save and passes every other test.
Feature gap F6.
*/
func TestSettingsViewRoundTripsThroughUpdate(t *testing.T) {
	initTestDB(t)
	gin.SetMode(gin.TestMode)
	router := gin.New()
	NewSettingController(router.Group("/panel/api"))

	before := postSettings(t, router, "/panel/api/setting/all", "{}")
	postSettings(t, router, "/panel/api/setting/update", string(before))
	after := postSettings(t, router, "/panel/api/setting/all", "{}")

	var was, is map[string]any
	if err := json.Unmarshal(before, &was); err != nil {
		t.Fatalf("view before: %v", err)
	}
	if err := json.Unmarshal(after, &is); err != nil {
		t.Fatalf("view after: %v", err)
	}
	if !reflect.DeepEqual(was, is) {
		for key := range was {
			if !reflect.DeepEqual(was[key], is[key]) {
				t.Errorf("%s: %v before the round trip, %v after", key, was[key], is[key])
			}
		}
		t.Fatal("posting the settings view back changed it")
	}
}
