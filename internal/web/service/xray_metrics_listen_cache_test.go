package service

import (
	"testing"
	"time"
)

/*
The metrics sampler reads the listen address from the template once per TTL.

Sample ran every 2 seconds and each run fetched the whole xray template from
the database -- twice a second per panel, whether or not anyone was watching
the dashboard -- to extract an address that changes only with a restart. F347.
*/
func TestMetricsListenIsCachedBetweenSamples(t *testing.T) {
	initTestDB(t)
	ss := SettingService{}
	if err := ss.setString("xrayTemplateConfig", `{"metrics":{"listen":"127.0.0.1:11111"}}`); err != nil {
		t.Fatalf("seed template: %v", err)
	}
	s := &XrayMetricsService{settingService: ss}
	if got, err := s.discoverListen(); err != nil || got != "127.0.0.1:11111" {
		t.Fatalf("first read = %q, %v", got, err)
	}

	if err := ss.setString("xrayTemplateConfig", `{"metrics":{"listen":"127.0.0.1:22222"}}`); err != nil {
		t.Fatalf("change template: %v", err)
	}
	if got, _ := s.discoverListen(); got != "127.0.0.1:11111" {
		t.Fatalf("read within the TTL = %q, want the cached address, not another template fetch", got)
	}
	s.listenAt = time.Time{}
	if got, _ := s.discoverListen(); got != "127.0.0.1:22222" {
		t.Fatalf("read after the TTL = %q, want the new address", got)
	}
}
