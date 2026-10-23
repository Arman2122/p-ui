package controller

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestWarpIntervalReportsClockWriteFailure(t *testing.T) {
	router := newNodeCredentialTestEngine(t)
	db := database.GetDB()
	if err := db.Create(&model.Setting{Key: "warpLastUpdate", Value: "0"}).Error; err != nil {
		t.Fatal(err)
	}
	rejected := errors.New("WARP clock write rejected")
	const callback = "test:reject_warp_clock"
	if err := db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if setting, ok := tx.Statement.Model.(*model.Setting); ok && setting.Key == "warpLastUpdate" {
			_ = tx.AddError(rejected)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Update().Remove(callback) })
	NewXraySettingController(router.Group("/panel/api"))
	req := httptest.NewRequest(http.MethodPost, "/panel/api/xray/warp/interval", strings.NewReader(url.Values{"interval": {"7"}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, req)
	if body := response.Body.String(); !strings.Contains(body, `"success":false`) || !strings.Contains(body, rejected.Error()) {
		t.Fatalf("clock failure was hidden: %s", body)
	}
}

func TestNodeProbeReportsHeartbeatWriteFailure(t *testing.T) {
	router := newNodeCredentialTestEngine(t)
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"obj":{"cpu":1,"mem":{"current":1,"total":2},"xray":{"version":"1","state":"running"},"panelVersion":"1.5.2","panelGuid":"guid","uptime":7}}`))
	}))
	defer remote.Close()
	host, portText, err := net.SplitHostPort(strings.TrimPrefix(remote.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	db := database.GetDB()
	node := &model.Node{Scheme: "http", Address: host, Port: port, BasePath: "/", Enable: true, AllowPrivateAddress: true}
	if err := db.Create(node).Error; err != nil {
		t.Fatal(err)
	}
	rejected := errors.New("heartbeat write rejected")
	const callback = "test:reject_probe_heartbeat"
	if err := db.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "nodes" {
			_ = tx.AddError(rejected)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Update().Remove(callback) })
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/panel/api/nodes/probe/"+strconv.Itoa(node.Id), nil))
	if body := response.Body.String(); !strings.Contains(body, `"success":false`) || !strings.Contains(body, rejected.Error()) {
		t.Fatalf("heartbeat failure was hidden: %s", body)
	}
}
