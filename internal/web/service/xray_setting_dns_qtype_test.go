package service

import (
	"encoding/json"
	"testing"
)

func TestSaveXraySettingDNSQTypeZero(t *testing.T) {
	initTestDB(t)
	svc := &XraySettingService{}
	raw := `{"outbounds":[{"tag":"dns-out","protocol":"dns","settings":{"rules":[{"action":"drop","qType":0},{"action":"direct"}]}}]}`
	if err := svc.SaveXraySetting(raw); err != nil {
		t.Fatal(err)
	}
	saved, err := svc.GetXrayConfigTemplate()
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Outbounds []struct {
			Settings struct {
				Rules []struct {
					QType any `json:"qType"`
				} `json:"rules"`
			} `json:"settings"`
		} `json:"outbounds"`
	}
	if err := json.Unmarshal([]byte(saved), &cfg); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Outbounds) != 1 || len(cfg.Outbounds[0].Settings.Rules) != 2 {
		t.Fatalf("saved DNS rules were changed: %s", saved)
	}
	if got := cfg.Outbounds[0].Settings.Rules[0].QType; got != "0" {
		t.Fatalf("qType = %#v, want string 0 so the core does not drop every query", got)
	}
}
