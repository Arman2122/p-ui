package outbound

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Arman2122/p-ui/internal/xray"
)

func TestTCPProbeAcceptsIPv6AndCoreProtocolSpelling(t *testing.T) {
	listener, err := net.Listen("tcp6", "[::1]:0")
	if err != nil {
		t.Skipf("IPv6 loopback unavailable: %v", err)
	}
	defer listener.Close()
	for _, protocol := range []string{"vless", "VLESS"} {
		payload := mustJSON(t, []any{map[string]any{"tag": "probe", "protocol": protocol, "settings": map[string]any{"address": "::1", "port": listener.Addr().(*net.TCPAddr).Port}}})
		results, err := (&OutboundService{}).TestOutbounds(payload, "", "", "tcp")
		if err != nil || len(results) != 1 || !results[0].Success {
			t.Fatalf("%s IPv6 TCP probe = %+v, error=%v", protocol, results, err)
		}
		if len(results[0].Endpoints) != 1 || results[0].Endpoints[0].Address != listener.Addr().String() {
			t.Fatalf("probe endpoint = %+v, want %s", results[0].Endpoints, listener.Addr())
		}
	}
}

func TestProbeProtocolCaseUsesCorrectLaneAndValidation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer srv.Close()
	withStubProcess(t, func(cfg *xray.Config, _ string) batchProcess { return &stubProcess{cfg: cfg, serveSocks: true} })
	withEgressTraceProbe(t, func(probeRoute) *TestEgressResult { return &TestEgressResult{IPv4: "198.51.100.2"} })
	for _, outbound := range []map[string]any{
		{"tag": "wg", "protocol": "WireGuard"},
		{"tag": "hy", "protocol": "HYSTERIA"},
		{"tag": "kcp", "protocol": "vless", "streamSettings": map[string]any{"network": "MKCP"}},
	} {
		results, err := (&OutboundService{}).TestOutbounds(mustJSON(t, []any{outbound}), srv.URL, "", "tcp")
		if err != nil || len(results) != 1 || !results[0].Success || results[0].Mode != "http" {
			t.Fatalf("UDP protocol/transport probe = %+v, error=%v", results, err)
		}
	}
	for protocol, want := range map[string]string{"Freedom": "Direct/DNS outbound cannot be tested", "DNS": "Direct/DNS outbound cannot be tested", "Loopback": "Loopback outbound cannot be tested", "Blackhole": "Blocked/blackhole outbound cannot be tested"} {
		results, err := (&OutboundService{}).TestOutbounds(mustJSON(t, []any{map[string]any{"tag": "probe", "protocol": protocol}}), srv.URL, "", "http")
		if err != nil || len(results) != 1 || results[0].Success || results[0].Error != want {
			t.Fatalf("%s probe = %+v, error=%v, want %q", protocol, results, err, want)
		}
	}
	cfg := buildBatchTestConfig([]*httpBatchItem{{tag: "wg", outbound: map[string]any{"tag": "wg", "protocol": "WireGuard"}}}, nil, []int{47519})
	var outbounds []struct {
		Tag      string `json:"tag"`
		Settings struct {
			NoKernelTun bool `json:"noKernelTun"`
		} `json:"settings"`
	}
	if err := json.Unmarshal(cfg.OutboundConfigs, &outbounds); err != nil {
		t.Fatal(err)
	}
	if len(outbounds) != 1 || !outbounds[0].Settings.NoKernelTun {
		t.Fatalf("WireGuard probe can create a kernel device: %+v", outbounds)
	}
}
