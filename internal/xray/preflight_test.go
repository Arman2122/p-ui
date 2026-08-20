package xray

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/util/json_util"
)

// binFolderWithXray points GetBinaryPath at a copy of the e2e binary, which is
// what TestConfig runs. Skipped unless XRAY_E2E_BINARY names one.
func binFolderWithXray(t *testing.T) {
	t.Helper()
	source := os.Getenv("XRAY_E2E_BINARY")
	if source == "" {
		t.Skip("set XRAY_E2E_BINARY to an xray binary to run this test")
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatalf("read %s: %v", source, err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, GetBinaryName()), data, 0o755); err != nil {
		t.Fatalf("stage the binary: %v", err)
	}
	t.Setenv("PUI_BIN_FOLDER", dir)
}

func rawJSON(t *testing.T, value any) json_util.RawMessage {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return json_util.RawMessage(body)
}

// loadableConfig is the smallest config the core accepts, and the base every
// case below breaks in exactly one way.
func loadableConfig(t *testing.T) *Config {
	t.Helper()
	return &Config{
		LogConfig: rawJSON(t, map[string]any{"loglevel": "warning"}),
		RouterConfig: rawJSON(t, map[string]any{
			"domainStrategy": "AsIs",
			"rules":          []any{map[string]any{"type": "field", "inboundTag": []string{"api"}, "outboundTag": "direct"}},
		}),
		InboundConfigs: []InboundConfig{{
			Listen:   json_util.RawMessage(`"127.0.0.1"`),
			Port:     62789,
			Protocol: "tunnel",
			Settings: rawJSON(t, map[string]any{"rewriteAddress": "127.0.0.1"}),
			Tag:      "api",
		}},
		OutboundConfigs: rawJSON(t, []any{
			map[string]any{"protocol": "freedom", "settings": map[string]any{}, "tag": "direct"},
			map[string]any{"protocol": "blackhole", "settings": map[string]any{}, "tag": "blocked"},
		}),
		Policy: rawJSON(t, map[string]any{}),
		API:    rawJSON(t, map[string]any{"services": []string{"HandlerService"}, "tag": "api"}),
		Stats:  rawJSON(t, map[string]any{}),
	}
}

// TestTestConfigAgreesWithTheCore is the whole point of the pre-flight: every
// case here is a refusal the core makes only at load time, which means the
// running process would be dead by the time the panel found out.
func TestTestConfigAgreesWithTheCore(t *testing.T) {
	binFolderWithXray(t)

	t.Run("a loadable config passes", func(t *testing.T) {
		if err := TestConfig(loadableConfig(t)); err != nil {
			t.Fatalf("the core refused a config it should load: %v", err)
		}
	})

	// The one refusal no config builder catches: it comes from registering the
	// handlers, which is why the routing compile can emit it undetected.
	t.Run("duplicate outbound tag", func(t *testing.T) {
		cfg := loadableConfig(t)
		cfg.OutboundConfigs = rawJSON(t, []any{
			map[string]any{"protocol": "freedom", "settings": map[string]any{}, "tag": "direct"},
			map[string]any{"protocol": "freedom", "settings": map[string]any{}, "tag": "pex4"},
			map[string]any{"protocol": "freedom", "settings": map[string]any{}, "tag": "pex4"},
		})
		assertRefused(t, TestConfig(cfg), "existing tag found: pex4")
	})

	t.Run("routing rule with no outbound tag", func(t *testing.T) {
		cfg := loadableConfig(t)
		cfg.RouterConfig = rawJSON(t, map[string]any{
			"domainStrategy": "AsIs",
			"rules":          []any{map[string]any{"type": "field", "domain": []string{"example.com"}, "outboundTag": ""}},
		})
		assertRefused(t, TestConfig(cfg), "neither outboundTag nor balancerTag is specified")
	})

	t.Run("routing criteria that is not a port", func(t *testing.T) {
		cfg := loadableConfig(t)
		cfg.RouterConfig = rawJSON(t, map[string]any{
			"domainStrategy": "AsIs",
			"rules":          []any{map[string]any{"type": "field", "port": "abc", "outboundTag": "direct"}},
		})
		assertRefused(t, TestConfig(cfg), "invalid port: abc")
	})

	t.Run("an inbound the builder refuses", func(t *testing.T) {
		cfg := loadableConfig(t)
		cfg.InboundConfigs = append(cfg.InboundConfigs, InboundConfig{
			Listen:   json_util.RawMessage(`"0.0.0.0"`),
			Port:     20003,
			Protocol: "shadowsocks",
			Settings: rawJSON(t, map[string]any{
				"method":   "2022-blake3-chacha20-poly1305",
				"password": "9a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f9",
				"network":  "tcp,udp",
				"clients": []any{map[string]any{
					"email":    "a@b",
					"password": "1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f708192a3b4c5d6e7f809",
				}},
			}),
			Tag: "ss2022-chacha",
		})
		assertRefused(t, TestConfig(cfg), "only blake3-aes-*-gcm methods are supported")
	})
}

// TestTestConfigWithoutABinaryIsNotARefusal: the pre-flight guards the running
// core, so being unable to ask must never be the reason a restart is blocked.
func TestTestConfigWithoutABinaryIsNotARefusal(t *testing.T) {
	t.Setenv("PUI_BIN_FOLDER", t.TempDir())
	if err := TestConfig(loadableConfig(t)); err != nil {
		t.Fatalf("a missing binary must not refuse a restart, got %v", err)
	}
}

func assertRefused(t *testing.T, err error, reason string) {
	t.Helper()
	if err == nil {
		t.Fatalf("the pre-flight accepted a config the core refuses (%s)", reason)
	}
	if !strings.Contains(err.Error(), reason) {
		t.Fatalf("error %q does not carry the core's reason %q", err, reason)
	}
}
