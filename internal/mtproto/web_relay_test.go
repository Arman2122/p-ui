package mtproto

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/logger"
)

func relayInstance() Instance {
	return Instance{
		Id: 7, Listen: "0.0.0.0", Port: 443,
		Secrets:                []SecretEntry{{Name: "alice", Secret: "ee11", AdTag: "0123456789abcdef0123456789abcdef"}},
		ThrottleMaxConnections: 5000,
		WebBackendEnable:       true, WebBackendPort: 2398,
		WebEnable: true, WebHost: "web.example.com", WebCoverDir: "/srv/site", WebBindPort: 8099, WebStatsPort: 8100,
	}
}

/*
The relay reads the same file as the proxy, so its table is rendered into it:
after the listener it dials, which gains proxy-protocol because the relay hands
the browser's address over PROXY v2, and before the trailing tables. A relay
missing any of what it refuses to start without renders nothing at all.
*/
func TestRenderConfigWebRelay(t *testing.T) {
	t.Setenv("PUI_BIN_FOLDER", "/opt/p-ui/bin")
	cfg := renderConfig(relayInstance(), 6000, "sesame")

	want := "\n[[listener]]\nbind-to = \"127.0.0.1:2398\"\naccept = [\"dd\", \"plain\"]\nproxy-protocol = true\n" +
		"\n[web]\nenabled = true\nhost = \"web.example.com\"\nbind-to = \"127.0.0.1:8099\"\nbackend = \"127.0.0.1:2398\"\n" +
		"token-key = \"/opt/p-ui/bin/mtproto/web-7.key\"\ncover-dir = \"/srv/site\"\nstats-bind-to = \"127.0.0.1:8100\"\n"
	if !strings.Contains(cfg, want) {
		t.Fatalf("the relay's tables are not rendered as one block:\n%s\nwant:\n%s", cfg, want)
	}
	if strings.Index(cfg, "[throttle]") > strings.Index(cfg, "[[listener]]") ||
		strings.Index(cfg, "[web]") > strings.Index(cfg, "[secret-ad-tags]") ||
		strings.Index(cfg, "[secret-ad-tags]") > strings.Index(cfg, "[secrets]") {
		t.Fatalf("section order must be throttle, listener, web, ad-tags, secrets:\n%s", cfg)
	}

	upstream := relayInstance()
	upstream.WebCoverDir, upstream.WebCoverUpstream = "", "http://127.0.0.1:8080"
	if cfg := renderConfig(upstream, 6000, ""); !strings.Contains(cfg, "cover-upstream = \"http://127.0.0.1:8080\"\n") || strings.Contains(cfg, "cover-dir") {
		t.Fatalf("an upstream cover renders as cover-upstream alone:\n%s", cfg)
	}

	for name, mutate := range map[string]func(*Instance){
		"relay off": func(i *Instance) { i.WebEnable = false },
		"no host":   func(i *Instance) { i.WebHost = "" },
		"no cover":  func(i *Instance) { i.WebCoverDir = "" },
		"both covers": func(i *Instance) {
			i.WebCoverUpstream = "http://127.0.0.1:8080"
		},
		"no bind port": func(i *Instance) { i.WebBindPort = 0 },
	} {
		half := relayInstance()
		mutate(&half)
		cfg := renderConfig(half, 6000, "")
		if strings.Contains(cfg, "[web]") || strings.Contains(cfg, "proxy-protocol = true") {
			t.Fatalf("%s: a relay that would refuse to start must render no [web] table and no PROXY listener:\n%s", name, cfg)
		}
	}
}

// Every relay field is structural: the relay refuses a changed one on reload
// and keeps running with what it has, so the panel restarts both processes.
func TestWebRelayFieldsAreStructural(t *testing.T) {
	base := relayInstance()
	for name, mutate := range map[string]func(*Instance){
		"webEnable":        func(i *Instance) { i.WebEnable = false },
		"webHost":          func(i *Instance) { i.WebHost = "other.example.com" },
		"webCoverDir":      func(i *Instance) { i.WebCoverDir = "/srv/other" },
		"webCoverUpstream": func(i *Instance) { i.WebCoverDir, i.WebCoverUpstream = "", "http://127.0.0.1:81" },
		"webBindPort":      func(i *Instance) { i.WebBindPort = 8199 },
		"webStatsPort":     func(i *Instance) { i.WebStatsPort = 8200 },
	} {
		changed := base
		mutate(&changed)
		if base.StructuralFingerprint() == changed.StructuralFingerprint() {
			t.Fatalf("structural fingerprint must change when %s changes", name)
		}
		if base.SecretsFingerprint() != changed.SecretsFingerprint() {
			t.Fatalf("secrets fingerprint must stay put when %s changes", name)
		}
	}
}

// The settings blob carries the relay's fields, and wanting the relay wants the
// backend listener it dials whether or not the form said so.
func TestApplySettingsReadsTheWebRelay(t *testing.T) {
	var inst Instance
	err := inst.ApplySettings(`{"webEnable":true,"webHost":" web.example.com ","webCoverUpstream":"http://127.0.0.1:8080",` +
		`"webBindPort":8099,"webStatsPort":8100,"webBackendPort":2398}`)
	if err != nil {
		t.Fatal(err)
	}
	if !inst.WebEnable || inst.WebHost != "web.example.com" || inst.WebCoverUpstream != "http://127.0.0.1:8080" ||
		inst.WebBindPort != 8099 || inst.WebStatsPort != 8100 {
		t.Fatalf("relay fields lost on the way in: %+v", inst)
	}
	if !inst.WebBackendEnable || inst.WebBackendPort != 2398 {
		t.Fatalf("the relay dials the backend listener, so wanting it must want that: %+v", inst)
	}
	if !inst.webRelayConfigured() {
		t.Fatal("a complete relay must be configured")
	}
}

// The signing key is written once, owner-only, and never rotated by a second
// call: a rotated key invalidates every live WEB session.
func TestEnsureWebKeyIsWrittenOnceOwnerOnly(t *testing.T) {
	t.Setenv("PUI_BIN_FOLDER", t.TempDir())
	if err := ensureWebKey(7); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(webKeyPath(7))
	if err != nil || len(first) != 32 {
		t.Fatalf("key = %d bytes (%v), want 32", len(first), err)
	}
	if info, _ := os.Stat(webKeyPath(7)); runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode %o, want 0600: the relay refuses a key others can read", info.Mode().Perm())
	}
	if err := ensureWebKey(7); err != nil {
		t.Fatal(err)
	}
	if second, _ := os.ReadFile(webKeyPath(7)); string(second) != string(first) {
		t.Fatal("a second ensure rotated the key")
	}
}

// The api port dodges every loopback port the WEB side owns, not only the
// backend's: a lost bind on any of them is silent because it is non-fatal.
func TestDrawAPIPortAvoidsEveryWebPort(t *testing.T) {
	draws := []int{2398, 8099, 8100, 9000}
	m := &Manager{drawPort: func() (int, error) {
		port := draws[0]
		draws = draws[1:]
		return port, nil
	}}
	port, err := m.drawAPIPort(2398, 8099, 8100)
	if err != nil || port != 9000 {
		t.Fatalf("drawAPIPort = %d, %v; want the first port clear of all three", port, err)
	}
}

// What the relay refuses at start is refused at save, with the rule named.
func TestValidateWebRelaySettings(t *testing.T) {
	for name, tc := range map[string]struct {
		settings string
		wantErr  string
	}{
		"relay off needs nothing":   {`{"webEnable":false}`, ""},
		"complete with a directory": {`{"webEnable":true,"webHost":"web.example.com","webCoverDir":"/srv/site"}`, ""},
		"complete with an upstream": {`{"webEnable":true,"webHost":"web.example.com","webCoverUpstream":"http://127.0.0.1:8080"}`, ""},
		"no host":                   {`{"webEnable":true,"webCoverDir":"/srv/site"}`, "needs a hostname"},
		"host is url":               {`{"webEnable":true,"webHost":"https://web.example.com","webCoverDir":"/srv/site"}`, "bare hostname"},
		"host is one label":         {`{"webEnable":true,"webHost":"web","webCoverDir":"/srv/site"}`, "fully qualified"},
		"no cover":                  {`{"webEnable":true,"webHost":"web.example.com"}`, "exactly one cover site"},
		"both covers":               {`{"webEnable":true,"webHost":"web.example.com","webCoverDir":"/srv","webCoverUpstream":"http://x"}`, "exactly one cover site"},
		"bad upstream":              {`{"webEnable":true,"webHost":"web.example.com","webCoverUpstream":"127.0.0.1:8080"}`, "http:// or https://"},
	} {
		t.Run(name, func(t *testing.T) {
			err := ValidateWebRelaySettings(tc.settings)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want no error, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want an error naming %q, got %v", tc.wantErr, err)
			}
		})
	}
}

// A relay that cannot start is retried by every supervise pass. The reason is
// said once and then carried by the Cores page, not by a warning every pass.
func TestARelayRefusalIsLoggedOncePerReason(t *testing.T) {
	t.Setenv("PUI_BIN_FOLDER", t.TempDir())
	inst := relayInstance()
	inst.Id = 7331
	m, cur := &Manager{}, &managed{}
	for range 3 {
		m.ensureRelayLocked(cur, inst)
	}
	if cur.relayCheck == "" {
		t.Fatal("with no binary to run the relay's check, the relay must be recorded as refused")
	}
	seen := 0
	for _, line := range logger.GetLogs(1000, "warning") {
		if strings.Contains(line, "WEB relay for inbound 7331") {
			seen++
		}
	}
	if seen != 1 {
		t.Fatalf("one refusal, repeated by three passes, was logged %d times; want once", seen)
	}
}
