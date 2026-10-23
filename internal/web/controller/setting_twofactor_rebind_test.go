package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Arman2122/p-ui/internal/web/service"
)

// GHSA-xqqw-jqqv-99h6: a save that keeps 2FA enabled must not be able to
// rebind the authenticator without presenting a current code.
func TestUpdateSettingRequiresCodeToReplaceTwoFactorToken(t *testing.T) {
	initTestDB(t)

	settingService := service.SettingService{}
	if err := settingService.SetTwoFactorToken("ORIGINALSECRET234567"); err != nil {
		t.Fatalf("seed token: %v", err)
	}
	if err := settingService.SetTwoFactorEnable(true); err != nil {
		t.Fatalf("seed enable: %v", err)
	}

	post := func(t *testing.T, mutate func(map[string]any)) string {
		t.Helper()
		base, err := settingService.GetAllSetting()
		if err != nil {
			t.Fatalf("GetAllSetting: %v", err)
		}
		raw, err := json.Marshal(base)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		body := map[string]any{}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		mutate(body)
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}

		gin.SetMode(gin.TestMode)
		router := gin.New()
		NewSettingController(router.Group("/panel/api"))
		req := httptest.NewRequest(http.MethodPost, "/panel/api/setting/update", strings.NewReader(string(payload)))
		req.Header.Set("Content-Type", "application/json")
		resp := httptest.NewRecorder()
		router.ServeHTTP(resp, req)
		return resp.Body.String()
	}

	t.Run("rebind without code is rejected", func(t *testing.T) {
		got := post(t, func(body map[string]any) {
			body["twoFactorEnable"] = true
			body["twoFactorToken"] = "ATTACKERSECRET567890"
		})
		if !strings.Contains(got, `"success":false`) {
			t.Fatalf("rebind without a 2FA code was accepted: %s", got)
		}
		stored, err := settingService.GetTwoFactorToken()
		if err != nil {
			t.Fatalf("GetTwoFactorToken: %v", err)
		}
		if stored != "ORIGINALSECRET234567" {
			t.Fatalf("stored 2FA secret = %q, want it unchanged", stored)
		}
	})

	t.Run("ordinary save with redacted token still succeeds", func(t *testing.T) {
		got := post(t, func(body map[string]any) {
			body["twoFactorEnable"] = true
			body["twoFactorToken"] = ""
		})
		if !strings.Contains(got, `"success":true`) {
			t.Fatalf("normal settings save was rejected: %s", got)
		}
		stored, err := settingService.GetTwoFactorToken()
		if err != nil {
			t.Fatalf("GetTwoFactorToken: %v", err)
		}
		if stored != "ORIGINALSECRET234567" {
			t.Fatalf("stored 2FA secret = %q, want it preserved", stored)
		}
	})

	t.Run("padded unchanged token is normalized before saving", func(t *testing.T) {
		got := post(t, func(body map[string]any) {
			body["twoFactorEnable"] = true
			body["twoFactorToken"] = " ORIGINALSECRET234567 \n"
		})
		if !strings.Contains(got, `"success":true`) {
			t.Fatalf("unchanged token was rejected: %s", got)
		}
		stored, err := settingService.GetTwoFactorToken()
		if err != nil || stored != "ORIGINALSECRET234567" {
			t.Fatalf("stored secret = %q, error = %v", stored, err)
		}
	})
}
