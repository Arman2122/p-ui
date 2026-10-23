package service

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/xray"
)

func TestRestartXrayKeepsRunningConfigOnGeneratedBindConflict(t *testing.T) {
	for _, force := range []bool{false, true} {
		name := "hot update"
		if force {
			name = "forced restart"
		}
		t.Run(name, func(t *testing.T) {
			svc, process := sessionDropCore(t)
			pid, config := process.PID(), process.GetConfig()
			written, err := os.ReadFile(xray.GetConfigPath())
			if err != nil {
				t.Fatal(err)
			}
			settings := &SettingService{}
			original, err := settings.GetXrayConfigTemplate()
			if err != nil {
				t.Fatal(err)
			}
			var template map[string]any
			if err := json.Unmarshal([]byte(original), &template); err != nil {
				t.Fatal(err)
			}
			rows := template["inbounds"].([]any)
			rows = append(rows, map[string]any{
				"tag": "generated-conflict", "listen": "0.0.0.0", "port": process.GetAPIPort(),
				"protocol": "mixed", "settings": map[string]any{"auth": "noauth", "udp": true},
			})
			template["inbounds"] = rows
			raw, err := json.Marshal(template)
			if err != nil {
				t.Fatal(err)
			}
			if err := settings.saveSetting("xrayTemplateConfig", string(raw)); err != nil {
				t.Fatal(err)
			}
			err = svc.RestartXray(force)
			if err == nil || !strings.Contains(err.Error(), "config refused") || !strings.Contains(err.Error(), "generated-conflict") {
				t.Errorf("generated collision returned %v", err)
			}
			if currentXrayProcess() != process || !process.IsRunning() || process.PID() != pid || !process.GetConfig().Equals(config) {
				t.Fatal("rejected generated config stopped or changed the running Xray")
			}
			after, err := os.ReadFile(xray.GetConfigPath())
			if err != nil || string(after) != string(written) {
				t.Fatalf("rejected desired config replaced its on-disk predecessor: %v", err)
			}
			if !strings.Contains(svc.GetXrayResult(), "config refused") {
				t.Fatalf("running Xray hides the rejected update: %q", svc.GetXrayResult())
			}
			if err := settings.saveSetting("xrayTemplateConfig", original); err != nil {
				t.Fatal(err)
			}
			if err := svc.RestartXray(false); err != nil || svc.GetXrayResult() != "" || process.PID() != pid {
				t.Fatalf("reverted desired config kept stale refusal or restarted Xray: %q, %v", svc.GetXrayResult(), err)
			}
		})
	}
}
