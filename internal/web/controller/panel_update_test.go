package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/v3/internal/config"

	"github.com/gin-gonic/gin"
)

// newPanelUpdateTestEngine registers only updatePanel/getUpdateStatus directly
// on the controller's zero value, bypassing NewServerController's cron/metrics
// setup (unrelated to these two handlers, and unnecessary weight for a unit
// test). Callers must set up a DB first (newHostTestDB(t)) since StartUpdate
// reads the dev-channel setting before doing anything else.
func newPanelUpdateTestEngine() *gin.Engine {
	a := &ServerController{}
	engine := gin.New()
	engine.GET("/panel/api/server/getUpdateStatus", a.getUpdateStatus)
	engine.POST("/panel/api/server/updatePanel", a.updatePanel)
	return engine
}

// isolateUpdateStatusFile gives one test exclusive, known-empty use of the
// panel self-update status file, and returns its path.
//
// Under `go test`, config.GetDBFolderPath() redirects to a writable temp folder
// private to this test binary, so the real /etc/p-ui is never read or written.
// That folder is still shared by every test in this package, so the file is
// removed both before the test and on cleanup: the "no update has ever run"
// case below must not be decided by what a sibling test wrote, and
// `go test -shuffle=on` in CI randomizes the order those run in.
func isolateUpdateStatusFile(t *testing.T) string {
	t.Helper()

	path := config.GetUpdateStatusFilePath()
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("clear %s: %v", path, err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
	return path
}

func doPanelUpdateReq(t *testing.T, engine *gin.Engine, method, path string) hostEnvelope {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("%s %s: status %d, body=%s", method, path, w.Code, w.Body.String())
	}
	var env hostEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("%s %s: decode envelope: %v body=%s", method, path, err, w.Body.String())
	}
	return env
}

// TestGetUpdateStatus_NoStatusFileYet exercises the read-only status endpoint
// with no prior update having run: it must report "pending" (not an error),
// since a missing status file is an expected, ordinary state, not a failure.
func TestGetUpdateStatus_NoStatusFileYet(t *testing.T) {
	newHostTestDB(t)
	isolateUpdateStatusFile(t)
	engine := newPanelUpdateTestEngine()

	env := doPanelUpdateReq(t, engine, http.MethodGet, "/panel/api/server/getUpdateStatus")
	if !env.Success {
		t.Fatalf("getUpdateStatus should always report success=true (it's a best-effort read): msg=%s", env.Msg)
	}
	var status struct {
		RunID string `json:"runId"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(env.Obj, &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status.State != "pending" {
		t.Fatalf("State = %q, want %q", status.State, "pending")
	}
}

// TestGetUpdateStatus_RunIdIsAlwaysAString is the regression test for the
// precision bug found in review: RunID is a 19-digit UnixNano timestamp, so
// it must round-trip over the wire as a JSON string, never a bare number -- a
// bare number would silently lose precision in JavaScript past
// Number.MAX_SAFE_INTEGER, breaking every future runId comparison on the
// frontend. Decoding into a Go string field below only succeeds if the wire
// value is actually a JSON string; a bare number there would fail to decode,
// so this test doubles as the wire-format check.
func TestGetUpdateStatus_RunIdIsAlwaysAString(t *testing.T) {
	newHostTestDB(t)
	statusPath := isolateUpdateStatusFile(t)
	engine := newPanelUpdateTestEngine()

	body := `{"runId":"1735689600123456789","state":"success","exitCode":0,"finishedAt":1735689612}`
	if err := os.WriteFile(statusPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	env := doPanelUpdateReq(t, engine, http.MethodGet, "/panel/api/server/getUpdateStatus")
	var status struct {
		RunID string `json:"runId"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(env.Obj, &status); err != nil {
		t.Fatalf("decode status (would fail here if runId were a bare JSON number instead of a string): %v, body=%s", err, env.Obj)
	}
	if status.RunID != "1735689600123456789" {
		t.Fatalf("RunID = %q, want %q", status.RunID, "1735689600123456789")
	}
	if status.State != "success" {
		t.Fatalf("State = %q, want %q", status.State, "success")
	}
}

// TestUpdatePanel_InvalidDevValueRejectedBeforeLaunch covers the one branch of
// updatePanel that's both untested and safe to exercise on any OS/CI runner:
// an unparseable "dev" form value is rejected by strconv.ParseBool before
// StartUpdateChannel (and therefore any real exec/network call) is ever
// reached, on Linux or otherwise.
func TestUpdatePanel_InvalidDevValueRejectedBeforeLaunch(t *testing.T) {
	newHostTestDB(t)
	engine := newPanelUpdateTestEngine()

	form := url.Values{"dev": {"notabool"}}
	req := httptest.NewRequest(http.MethodPost, "/panel/api/server/updatePanel", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s", w.Code, w.Body.String())
	}
	var env hostEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode envelope: %v body=%s", err, w.Body.String())
	}
	if env.Success {
		t.Fatal("updatePanel with dev=notabool: success = true, want false")
	}
	if len(env.Obj) != 0 && string(env.Obj) != "null" {
		t.Fatalf("updatePanel error response must not carry an obj/runId: got %s", env.Obj)
	}
}
