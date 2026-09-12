package service

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/Arman2122/p-ui/internal/xray"
)

func TestDefaultSettingsUseConfiguredLogWithoutGeneratedConfig(t *testing.T) {
	initTestDB(t)
	t.Setenv("PUI_BIN_FOLDER", t.TempDir())
	s := &SettingService{}
	if err := s.saveSetting("datepicker", "jalalian"); err != nil {
		t.Fatal(err)
	}
	if err := s.saveSetting("pageSize", "37"); err != nil {
		t.Fatal(err)
	}
	check := func(t *testing.T, want bool) {
		t.Helper()
		result, err := s.GetDefaultSettings("panel.example.test:2053")
		if err != nil {
			t.Fatalf("defaults without generated config: %v", err)
		}
		defaults, ok := result.(map[string]any)
		if !ok {
			t.Fatalf("defaults type = %T", result)
		}
		if defaults["accessLogEnable"] != want || defaults["datepicker"] != "jalalian" || defaults["pageSize"] != 37 {
			t.Fatalf("defaults lost configured values: accessLogEnable=%v datepicker=%v pageSize=%v", defaults["accessLogEnable"], defaults["datepicker"], defaults["pageSize"])
		}
	}
	t.Run("fresh shipped template", func(t *testing.T) { check(t, false) })
	for _, tc := range []struct {
		name, template string
		enabled        bool
	}{
		{"configured file", `{"log":{"access":"/var/log/p-ui/access.log"}}`, true},
		{"disabled", `{"log":{"access":"none"}}`, false},
		{"console", `{"log":{"access":""}}`, false},
		{"omitted", `{}`, false},
		{"null", `{"log":null}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := s.saveSetting("xrayTemplateConfig", tc.template); err != nil {
				t.Fatal(err)
			}
			check(t, tc.enabled)
		})
	}
	if _, err := xray.GetAccessLogPath(); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runtime log reader no longer requires generated config: %v", err)
	}
	if err := s.saveSetting("xrayTemplateConfig", `{"log":`); err != nil {
		t.Fatal(err)
	}
	_, err := s.GetDefaultSettings("panel.example.test")
	var malformed *json.SyntaxError
	if !errors.As(err, &malformed) {
		t.Fatalf("invalid persisted template was hidden: %v", err)
	}
	if err := os.WriteFile(xray.GetConfigPath(), []byte(`{"log":{"access":"none"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.saveSetting("xrayTemplateConfig", `{"log":{"access":"/var/log/p-ui/access.log"}}`); err != nil {
		t.Fatal(err)
	}
	check(t, true)
	if runtimePath, err := xray.GetAccessLogPath(); err != nil || runtimePath != "none" {
		t.Fatalf("runtime reader stopped using generated config: %q, %v", runtimePath, err)
	}
}
