package ssh

import (
	"context"
	"fmt"
	"runtime/debug"
	"strings"

	"github.com/Arman2122/p-ui/internal/config"
)

const sshEngineModule = "golang.org/x/crypto"

// SSH is compiled into the panel; its compatibility banner is not its engine version.
func (c *Core) Installed(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	info, _ := debug.ReadBuildInfo()
	return embeddedSSHVersion(config.GetPanelVersion(), info), nil
}

func embeddedSSHVersion(panelVersion string, info *debug.BuildInfo) string {
	engine := sshEngineModule + " version unknown"
	if info != nil {
		for _, dependency := range info.Deps {
			if dependency == nil || dependency.Path != sshEngineModule {
				continue
			}
			module := dependency
			if module.Replace != nil {
				module = module.Replace
				engine = sshEngineModule + " local replacement; version unknown"
			}
			if module.Version != "" && module.Version != "(devel)" {
				engine = sshEngineModule + " " + module.Version
				if dependency.Replace != nil {
					engine = sshEngineModule + " replaced by " + module.Path + " " + module.Version
				}
			}
			break
		}
	}
	panelVersion = strings.TrimSpace(panelVersion)
	if panelVersion == "" {
		panelVersion = "version unknown"
	}
	return fmt.Sprintf("Penhoon SSH %s (%s)", panelVersion, engine)
}
