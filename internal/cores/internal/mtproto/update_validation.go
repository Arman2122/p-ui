package mtproto

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	engine "github.com/Arman2122/p-ui/internal/mtproto"
)

// validateUpdateConfig asks the candidate about a file carrying every key the
// panel can write, so a release missing one is refused with no inbound running.
func validateUpdateConfig(ctx context.Context, binary string) error {
	dir, err := os.MkdirTemp("", "mtg-update-check-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	inst := engine.Instance{
		Id: 1, Tag: "update-check", Listen: "127.0.0.1", Port: 19001,
		Secrets: []engine.SecretEntry{{
			Name: "update-check", Secret: engine.MintFakeTLSSecret(""), AdTag: "0123456789abcdef0123456789abcdef",
			QuotaBytes: 1 << 30, ExpiresUnix: 4102444800, SpeedUpBytesPerSecond: 125000, SpeedDownBytesPerSecond: 250000, IPLimit: 3,
		}},
		ThrottleMaxConnections: 5000, ProxyProtocolListener: true, RouteThroughXray: true, XrayRoutePort: 19004,
		WebBackendEnable: true, WebBackendPort: 19002, WebEnable: true, WebHost: "web.example.com",
		WebCoverUpstream: "127.0.0.1:19005", WebBindPort: 19006, WebStatsPort: 19007,
	}
	path := filepath.Join(dir, "mtproto.toml")
	if err := os.WriteFile(path, []byte(engine.RenderConfig(inst, 19003, "update-check")), 0o600); err != nil {
		return err
	}
	if err := engine.CheckConfig(ctx, binary, path); err != nil {
		return fmt.Errorf("mtproto: update cannot run the panel's config; installed binary preserved: %w", err)
	}
	return nil
}

// validateRunningConfigs asks the candidate about every file a sidecar runs off
// now, relay included, so an install never lands a binary a live inbound refuses.
func validateRunningConfigs(ctx context.Context, binary string, configs []engine.RunningConfig) error {
	for _, cfg := range configs {
		if err := engine.CheckConfig(ctx, binary, cfg.Path); err != nil {
			return fmt.Errorf("mtproto: update cannot run inbound %d; installed binary preserved: %w", cfg.ID, err)
		}
		if !cfg.WebRelay {
			continue
		}
		if err := engine.CheckWebRelay(ctx, binary, cfg.Path); err != nil {
			return fmt.Errorf("mtproto: update cannot run the WEB relay of inbound %d; installed binary preserved: %w", cfg.ID, err)
		}
	}
	return nil
}
