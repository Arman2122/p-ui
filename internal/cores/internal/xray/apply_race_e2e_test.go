package xray

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	engine "github.com/Arman2122/p-ui/internal/xray"
)

/*
Two concurrent applies must not each undo the other.

ApplyInstance reads the running config, splices its own inbound into a copy, and
pushes the whole result back. Two of them interleaving both read the SAME
starting config, so the second push carries a version that never knew about the
first -- and hotApply's diff, computed against that stale base, gRPC-DELETES the
inbound the first one had just added. The client is gone from the live core
while the database still says it is there, until something forces a full restart.

`make race` cannot see this: GetConfig and SetConfig are each mutex-guarded
individually, so there is no data race to detect. It is a lost update, and only
a test that drives the real gRPC surface can catch it -- hotApply needs a live
HandlerService for any non-empty diff, and ApplyInstance refuses to run at all
unless the process is really running.

Both sides of the race are real Core methods, so nothing here is hand-rolled and
nothing runs outside the lock once the fix is in. F238.
*/
func TestConcurrentApplyInstancesDoNotDeleteEachOther(t *testing.T) {
	bin := os.Getenv("XRAY_E2E_BINARY")
	if bin == "" {
		t.Skip("set XRAY_E2E_BINARY to an xray binary to run this test")
	}

	dir := t.TempDir()
	staged := filepath.Join(dir, engine.GetBinaryName())
	raw, err := os.ReadFile(bin)
	if err != nil {
		t.Fatalf("read %s: %v", bin, err)
	}
	if err := os.WriteFile(staged, raw, 0o755); err != nil {
		t.Fatalf("stage the binary: %v", err)
	}
	t.Setenv("PUI_BIN_FOLDER", dir)

	apiPort := freeTCPPort(t)
	cfg := baseConfigForRace(t, apiPort)

	process := engine.NewProcess(cfg)
	if err := process.Start(); err != nil {
		t.Fatalf("start xray: %v", err)
	}
	t.Cleanup(func() { _ = process.Stop() })

	// The manager is a process-wide singleton; this test owns it while it runs.
	engine.GetManager().Replace(process)
	t.Cleanup(func() { engine.GetManager().Replace(nil) })

	waitForPort(t, apiPort)

	c := New(Deps{BaseConfig: func() (*engine.Config, error) { return cfg, nil }})

	const n = 16
	instances := make([]core.Instance, n)
	for i := range instances {
		instances[i] = core.Instance{
			Tag:    fmt.Sprintf("race-%d", i),
			Kind:   "vless",
			Enable: true,
			Port:   freeTCPPort(t),
			Listen: "127.0.0.1",
			Settings: fmt.Sprintf(
				`{"clients":[{"id":"11111111-2222-4333-8444-%012d","email":"race-%d@e2e"}],"decryption":"none"}`, i, i),
			StreamSettings: `{"network":"tcp","security":"none"}`,
		}
	}

	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range instances {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = c.ApplyInstance(context.Background(), instances[i])
		}(i)
	}
	close(start)
	wg.Wait()

	live := map[string]bool{}
	for _, ib := range engine.GetManager().Current().GetConfig().InboundConfigs {
		live[ib.Tag] = true
	}
	var missing []string
	for i := range instances {
		if errs[i] != nil {
			t.Errorf("%s: %v", instances[i].Tag, errs[i])
			continue
		}
		if !live[instances[i].Tag] {
			missing = append(missing, instances[i].Tag)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("%d of %d inbounds reported applied are absent from the running core: %v.\n"+
			"Each was deleted by a sibling apply whose config snapshot predated it; the panel believes they are live",
			len(missing), n, missing)
	}
}

func baseConfigForRace(t *testing.T, apiPort int) *engine.Config {
	t.Helper()
	raw := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"api": map[string]any{
			"services": []string{"HandlerService", "StatsService", "RoutingService"},
			"tag":      "api",
		},
		"inbounds": []any{map[string]any{
			"listen": "127.0.0.1", "port": apiPort, "protocol": "tunnel",
			"settings": map[string]any{"rewriteAddress": "127.0.0.1"}, "tag": "api",
		}},
		"outbounds": []any{
			map[string]any{"protocol": "freedom", "settings": map[string]any{}, "tag": "direct"},
			map[string]any{"protocol": "blackhole", "settings": map[string]any{}, "tag": "blocked"},
		},
		"routing": map[string]any{
			"domainStrategy": "AsIs",
			"rules": []any{map[string]any{
				"type": "field", "inboundTag": []string{"api"}, "outboundTag": "api",
			}},
		},
		"policy": map[string]any{"levels": map[string]any{"0": map[string]any{"statsUserOnline": true}}},
		"stats":  map[string]any{},
	}
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &engine.Config{}
	if err := json.Unmarshal(b, cfg); err != nil {
		t.Fatalf("unmarshal base config: %v", err)
	}
	return cfg
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func waitForPort(t *testing.T, port int) {
	t.Helper()
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	for range 100 {
		conn, err := net.DialTimeout("tcp", addr, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("xray api port %d never came up", port)
}
