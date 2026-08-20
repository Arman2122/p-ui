package xray

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/util/common"
)

// xrayTestTimeout bounds the pre-flight loader run. A cold geodata load is the
// slow case and still lands well inside a second on the boxes this panel runs on.
var xrayTestTimeout = 30 * time.Second

/*
TestConfig asks the pinned xray binary whether it would accept cfg, through the
binary's own `-test` mode: exactly the load a start performs — config parse,
every inbound and outbound built, every handler registered, duplicate tags
included — minus binding the ports.

It exists because xray-core loads atomically: one malformed inbound, routing rule
or outbound aborts the whole core, so a config that fails here would take every
inbound of every protocol offline rather than only the row that is wrong.

Being unable to ask is not a refusal. No binary, no permission, a timeout: the
answer is nil, because this check must never become the reason a restart cannot
happen.
*/
func TestConfig(cfg *Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return common.NewErrorf("could not encode the generated xray config: %v", err)
	}
	path, err := writePreflightConfig(data)
	if err != nil {
		logger.Warning("xray config pre-flight skipped:", err)
		return nil
	}
	defer func() { _ = os.Remove(path) }()

	ctx, cancel := context.WithTimeout(context.Background(), xrayTestTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, GetBinaryPath(), "-test", "-c", path).CombinedOutput()
	if err == nil {
		return nil
	}
	if ctx.Err() != nil || !errors.As(err, new(*exec.ExitError)) {
		logger.Warning("xray config pre-flight skipped:", err)
		return nil
	}
	return common.NewError(coreRefusal(out))
}

// writePreflightConfig puts the candidate beside the real config: the same
// filesystem and directory permissions, and 0600 because it carries every
// client's credentials.
func writePreflightConfig(data []byte) (string, error) {
	tmp, err := os.CreateTemp(filepath.Dir(GetConfigPath()), ".preflight-*.json")
	if err != nil {
		return "", err
	}
	path := tmp.Name()
	err = tmp.Chmod(0o600)
	if err == nil {
		_, err = tmp.Write(data)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(path)
		return "", err
	}
	return path, nil
}

// coreRefusal picks the one line xray prints for the operator out of the banner
// and the loader chatter around it.
func coreRefusal(out []byte) string {
	for _, line := range strings.Split(string(out), "\n") {
		if after, ok := strings.CutPrefix(strings.TrimSpace(line), "Failed to start: "); ok {
			return after
		}
	}
	if trimmed := strings.TrimSpace(string(out)); trimmed != "" {
		return trimmed
	}
	return "xray-core rejected the generated config"
}
