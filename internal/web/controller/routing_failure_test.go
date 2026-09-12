package controller

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/routing"
	"github.com/Arman2122/p-ui/internal/web/locale"
	"github.com/Arman2122/p-ui/internal/web/service"
)

func routingFailureRouter(t *testing.T) (*gin.Engine, *bool) {
	t.Helper()
	newHostTestDB(t)
	xray := &service.XrayService{}
	wasDirty := xray.IsNeedRestartAndSetFalse()
	t.Cleanup(func() {
		xray.IsNeedRestartAndSetFalse()
		if wasDirty {
			xray.SetToNeedRestart()
		}
	})
	engine := gin.New()
	var success bool
	engine.Use(func(c *gin.Context) {
		c.Set("I18n", func(_ locale.I18nType, key string, _ ...string) string { return "translated:" + key })
		c.Next()
		success = c.GetBool(activitySuccessKey)
	})
	NewRoutingController(engine.Group("/routing"))
	return engine, &success
}

func TestRoutingControllerConvergenceFailureRetainsSavedRule(t *testing.T) {
	engine, activitySuccess := routingFailureRouter(t)
	db := database.GetDB()
	failure := errors.New("routing convergence unavailable")
	written := false
	markWritten := func(tx *gorm.DB) {
		if tx.Statement.Table == "routing_rules" && tx.Error == nil {
			written = true
		}
	}
	const callback = "test:routing_convergence_failure"
	for _, processor := range []interface {
		Register(string, func(*gorm.DB)) error
		Remove(string) error
	}{db.Callback().Create(), db.Callback().Update(), db.Callback().Delete()} {
		if err := processor.Register(callback, markWritten); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = processor.Remove(callback) })
	}
	// Stop before either convergence path can reach the host network stack.
	if err := db.Callback().Query().Before("gorm:query").Register(callback, func(tx *gorm.DB) {
		if written && (tx.Statement.Table == "inbounds" || tx.Statement.Table == "egresses") {
			tx.AddError(failure)
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Callback().Query().Remove(callback) })
	rule := model.RoutingRule{Enable: true, IngressScope: model.RoutingScopeAll, DestKind: model.RoutingDestBlock}
	var id int
	for _, operation := range []string{"add", "update", "order", "delete"} {
		t.Run(operation, func(t *testing.T) {
			path := "/routing/rules"
			var body any = rule
			switch operation {
			case "update":
				path += "/" + strconv.Itoa(id)
				rule.Remark = "retry the saved rule"
				body = rule
			case "order":
				path += "/order"
				body = map[string]any{"ids": []int{id}}
			case "delete":
				path += "/" + strconv.Itoa(id) + "/del"
				body = nil
			}
			written = false
			response := doHostReq(t, engine, http.MethodPost, path, body)
			if !written || response.Success || *activitySuccess || !strings.Contains(response.Msg, failure.Error()) {
				t.Fatalf("written=%v, activity success=%v, response=%+v", written, *activitySuccess, response)
			}
			if !(&service.XrayService{}).IsNeedRestartAndSetFalse() {
				t.Error("convergence failure cleared the pending restart")
			}
			var stored []model.RoutingRule
			if err := db.Find(&stored).Error; err != nil {
				t.Fatal(err)
			}
			if operation == "delete" {
				if len(stored) != 0 {
					t.Fatalf("deleted rule remains: %+v", stored)
				}
				return
			}
			if len(stored) != 1 || stored[0].Remark != rule.Remark {
				t.Fatalf("persisted rules=%+v, want exactly one with remark %q", stored, rule.Remark)
			}
			if operation == "order" {
				return
			}
			var saved model.RoutingRule
			if err := json.Unmarshal(response.Obj, &saved); err != nil {
				t.Fatal(err)
			}
			if saved.Id == 0 || saved.Id != stored[0].Id || saved.Remark != rule.Remark {
				t.Errorf("failure object=%+v, persisted rule=%+v", saved, stored[0])
			}
			if operation == "update" && saved.Id != id {
				t.Errorf("retry changed rule id: %d -> %d", id, saved.Id)
			}
			id = stored[0].Id
		})
	}
}

func TestRoutingControllerRefusedSaveIsFailure(t *testing.T) {
	engine, activitySuccess := routingFailureRouter(t)
	db := database.GetDB()
	inbound := model.Inbound{Enable: true, Tag: "routing-refusal", Protocol: model.VLESS, Port: 45901, Settings: `{"clients":[]}`}
	if err := db.Create(&inbound).Error; err != nil {
		t.Fatal(err)
	}
	stored := model.RoutingRule{Enable: true, IngressScope: model.RoutingScopeAll, DestKind: model.RoutingDestBlock, Remark: "unchanged"}
	if err := db.Create(&stored).Error; err != nil {
		t.Fatal(err)
	}
	missingExit := 999
	for _, path := range []string{"/routing/rules", "/routing/rules/" + strconv.Itoa(stored.Id)} {
		for _, invalid := range []string{"compile", "shape"} {
			t.Run(path+"/"+invalid, func(t *testing.T) {
				rule := model.RoutingRule{
					Enable: true, IngressScope: model.RoutingScopeSelected,
					IngressIds: "[" + strconv.Itoa(inbound.Id) + "]",
					DestKind:   model.RoutingDestExit, DestExitId: &missingExit,
				}
				wantMessage := "translated:" + routing.KeyExitMissing
				if invalid == "shape" {
					rule.IngressScope = "invalid"
					wantMessage = `"invalid" is not an ingress scope`
				}
				response := doHostReq(t, engine, http.MethodPost, path, rule)
				if response.Success || *activitySuccess || string(response.Obj) != "null" || !strings.Contains(response.Msg, wantMessage) {
					t.Errorf("refusal response=%+v, activity success=%v", response, *activitySuccess)
				}
				var rows []model.RoutingRule
				if err := db.Find(&rows).Error; err != nil {
					t.Fatal(err)
				}
				if len(rows) != 1 || rows[0].Id != stored.Id || rows[0].Remark != stored.Remark || rows[0].DestKind != stored.DestKind {
					t.Fatalf("refused save changed persisted rules: %+v", rows)
				}
				if (&service.XrayService{}).IsNeedRestartAndSetFalse() {
					t.Error("refused save armed a restart")
				}
			})
		}
	}
}
