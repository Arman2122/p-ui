package service

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/xray"
)

func lifecycleFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func waitForPort(t *testing.T, port int) {
	t.Helper()
	for range 100 {
		if c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond); err == nil {
			c.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("port %d never opened", port)
}

// The core resolves geoip.dat/geosite.dat next to its own executable, which is
// where the panel keeps them; the template ships a geoip:private routing rule.
func coreCommand(t *testing.T, configPath string) *exec.Cmd {
	t.Helper()
	binary := os.Getenv("XRAY_E2E_BINARY")
	cmd := exec.Command(binary, "-c", configPath)
	cmd.Env = append(os.Environ(), "XRAY_LOCATION_ASSET="+filepath.Dir(binary))
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	return cmd
}

/*
startCoreFromPanelConfig writes the config the panel generated and runs the real
binary on it.

Four narrow edits, each because the test shares a machine and has to reach a
loopback sink — not because the panel is wrong about any of them. The api and
metrics listeners move off their fixed ports so a panel already running here
keeps them; the freedom outbound's finalRules and the template's
private-destination routing rule stop blackholing 127.0.0.1, which is the only
address a sink can have here.

Everything else is the config the panel generated, including the client list
RenderInbound decided on — which is the thing under test.
*/
func startCoreFromPanelConfig(t *testing.T, dir string, cfg *xray.Config, generation int) *exec.Cmd {
	t.Helper()

	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal the generated config: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("reparse the generated config: %v", err)
	}
	delete(doc, "metrics")
	inbounds, _ := doc["inbounds"].([]any)
	for _, entry := range inbounds {
		in, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if tag, _ := in["tag"].(string); tag == "api" {
			in["port"] = lifecycleFreePort(t)
		}
	}
	outbounds, _ := doc["outbounds"].([]any)
	for _, entry := range outbounds {
		out, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		if protocol, _ := out["protocol"].(string); protocol != "freedom" {
			continue
		}
		out["settings"] = map[string]any{
			"finalRules": []any{map[string]any{"action": "allow"}},
		}
	}

	routing, _ := doc["routing"].(map[string]any)
	if rules, ok := routing["rules"].([]any); ok {
		kept := make([]any, 0, len(rules))
		for _, entry := range rules {
			rule, ok := entry.(map[string]any)
			if !ok {
				kept = append(kept, entry)
				continue
			}
			ips, _ := rule["ip"].([]any)
			private := false
			for _, ip := range ips {
				if token, _ := ip.(string); token == "geoip:private" {
					private = true
				}
			}
			if private {
				continue
			}
			kept = append(kept, entry)
		}
		routing["rules"] = kept
	}

	body, err := json.MarshalIndent(doc, "", " ")
	if err != nil {
		t.Fatalf("re-encode the config: %v", err)
	}
	path := filepath.Join(dir, fmt.Sprintf("server-%d.json", generation))
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write the config: %v", err)
	}

	cmd := coreCommand(t, path)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the core: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	return cmd
}

func waitForPortFree(t *testing.T, port int) {
	t.Helper()
	for range 100 {
		if l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port)); err == nil {
			l.Close()
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("port %d never came free", port)
}

/*
restartCore is what the panel does after a change it cannot hot-apply, and the
reason this test does it explicitly: the running core was built from the config
as it was, so nothing a reset writes reaches a client until the config is
regenerated and the process replaced.
*/
func restartCore(t *testing.T, dir string, cfg *xray.Config, generation int, previous *exec.Cmd, port int) *exec.Cmd {
	t.Helper()
	if previous != nil {
		_ = previous.Process.Kill()
		_, _ = previous.Process.Wait()
		waitForPortFree(t, port)
	}
	next := startCoreFromPanelConfig(t, dir, cfg, generation)
	waitForPort(t, port)
	return next
}

// startTunnelClient runs a second core as the client: an HTTP inbound routed out
// through vless to the panel's inbound, which is how a real client reaches it.
func startTunnelClient(t *testing.T, dir string, serverPort int, uuid string) int {
	t.Helper()
	httpPort := lifecycleFreePort(t)

	cfg := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"inbounds": []any{map[string]any{
			"listen": "127.0.0.1", "port": httpPort, "protocol": "http", "settings": map[string]any{},
		}},
		"outbounds": []any{map[string]any{
			"protocol": "vless",
			"settings": map[string]any{"vnext": []any{map[string]any{
				"address": "127.0.0.1", "port": serverPort,
				"users": []any{map[string]any{"id": uuid, "encryption": "none"}},
			}}},
			"streamSettings": map[string]any{"network": "tcp", "security": "none"},
		}},
	}
	body, err := json.MarshalIndent(cfg, "", " ")
	if err != nil {
		t.Fatalf("marshal the client config: %v", err)
	}
	path := filepath.Join(dir, "client.json")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatalf("write the client config: %v", err)
	}

	cmd := coreCommand(t, path)
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the tunnel client: %v", err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	waitForPort(t, httpPort)
	return httpPort
}

// reachesThroughTunnel reports whether the client can actually carry a request,
// which is the only statement about access that matters.
func reachesThroughTunnel(proxyPort int, target string) bool {
	proxy, err := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", proxyPort))
	if err != nil {
		return false
	}
	client := &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxy), DisableKeepAlives: true},
		Timeout:   8 * time.Second,
	}
	resp, err := client.Get(target)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode == http.StatusOK && string(body) == "reached"
}

/*
The whole claim, end to end, against the real core.

The finding is not "three columns disagree" — it is that a paying client whose
quota renews still cannot connect. Every intermediate step here was already true
before the fix: the rows looked reset, the panel showed a fresh allowance, the
Clients page showed the client enabled. Only the last step was false, and only a
real connection can tell you that.

Skipped unless XRAY_E2E_BINARY names an xray built from the version go.mod pins.
*/
func TestQuotaResetRestoresAccessEndToEnd(t *testing.T) {
	if os.Getenv("XRAY_E2E_BINARY") == "" {
		t.Skip("set XRAY_E2E_BINARY to an xray binary to run this test")
	}
	initTestDB(t)
	dir := t.TempDir()

	const uuid = "11111111-2222-3333-4444-555555555555"
	const email = "renewed@example.com"
	inboundPort := lifecycleFreePort(t)

	sink := lifecycleFreePort(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/probe", func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, "reached") })
	server := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", sink), Handler: mux, ReadHeaderTimeout: time.Second}
	go func() { _ = server.ListenAndServe() }()
	t.Cleanup(func() { _ = server.Close() })
	waitForPort(t, sink)
	target := fmt.Sprintf("http://127.0.0.1:%d/probe", sink)

	settings, err := json.Marshal(map[string]any{
		"clients":    []any{map[string]any{"id": uuid, "email": email, "enable": true}},
		"decryption": "none",
	})
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	inbound := &model.Inbound{
		UserId: 1, Enable: true, Tag: "in-lifecycle", Port: inboundPort, Protocol: model.VLESS,
		Listen: "127.0.0.1", Settings: string(settings),
		StreamSettings: `{"network":"tcp","security":"none"}`,
	}
	inboundSvc := &InboundService{}
	if _, _, _, err := inboundSvc.AddInbound(inbound); err != nil {
		t.Fatalf("AddInbound: %v", err)
	}

	xraySvc := &XrayService{}
	cfg, err := xraySvc.GetXrayConfig()
	if err != nil {
		t.Fatalf("GetXrayConfig: %v", err)
	}
	running := restartCore(t, dir, cfg, 1, nil, inboundPort)
	proxyPort := startTunnelClient(t, dir, inboundPort, uuid)

	if !reachesThroughTunnel(proxyPort, target) {
		t.Fatal("the client could not connect before its quota was spent; the rest of this test proves nothing")
	}

	// Spend the allowance and let the panel's own depletion pass act on it.
	if err := database.GetDB().Model(core.ClientTraffic{}).Where("email = ?", email).
		Updates(map[string]any{"total": 100, "up": 60, "down": 60}).Error; err != nil {
		t.Fatalf("spend the allowance: %v", err)
	}
	// The whole pass, because the settings-JSON sync moved after the commit.
	if _, _, err := inboundSvc.AddTraffic(nil, nil); err != nil {
		t.Fatalf("AddTraffic: %v", err)
	}

	depletedCfg, err := xraySvc.GetXrayConfig()
	if err != nil {
		t.Fatalf("GetXrayConfig after depletion: %v", err)
	}
	running = restartCore(t, dir, depletedCfg, 2, running, inboundPort)

	if reachesThroughTunnel(proxyPort, target) {
		t.Fatal("a depleted client still reaches the internet; the depletion pass is not what this test is about, but it has to hold for the rest to mean anything")
	}

	// The panel's own reset, not a hand-written UPDATE.
	if err := (&ClientService{}).ResetAllClientTraffics(inboundSvc, inbound.Id); err != nil {
		t.Fatalf("ResetAllClientTraffics: %v", err)
	}

	renewedCfg, err := xraySvc.GetXrayConfig()
	if err != nil {
		t.Fatalf("GetXrayConfig after the reset: %v", err)
	}
	restartCore(t, dir, renewedCfg, 3, running, inboundPort)

	if !reachesThroughTunnel(proxyPort, target) {
		t.Fatal("the client's quota was reset and it still cannot connect: every row reads restored and the core has no such user")
	}
}
