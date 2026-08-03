// Package config provides configuration management utilities for the Penhoon UI panel,
// including version information, logging levels, on-disk paths, and environment
// variable handling.
package config

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

//go:embed version
var version string

//go:embed name
var name string

// buildCommit and buildDate are injected at build time via `-ldflags -X` for
// CI per-commit (dev channel) builds; see .github/workflows/release.yml. They
// stay empty for a plain `go build` and for stable tagged releases, which is how
// IsDevBuild tells a rolling dev build apart from a stable/local one.
var (
	buildCommit string
	buildDate   string
)

// LogLevel represents the logging level for the application.
type LogLevel string

// Logging level constants
const (
	Debug   LogLevel = "debug"
	Info    LogLevel = "info"
	Notice  LogLevel = "notice"
	Warning LogLevel = "warning"
	Error   LogLevel = "error"
)

// GetBaseVersion returns the raw embedded release version of the Penhoon UI panel
// (e.g. "1.0.0"). This is the panel's own version, not the Xray version. For the
// version a panel advertises/displays (which adds a "dev+<sha>" label on dev
// builds), use GetPanelVersion.
func GetBaseVersion() string {
	return strings.TrimSpace(version)
}

// GetName returns the short application name ("p-ui") used for on-disk names
// such as the service and log files.
func GetName() string {
	return strings.TrimSpace(name)
}

// GetBuildCommit returns the short git commit this binary was built from, or an
// empty string for a plain/local build or a stable tagged release.
func GetBuildCommit() string {
	return strings.TrimSpace(buildCommit)
}

// GetBuildDate returns the UTC build timestamp injected at build time, or empty.
func GetBuildDate() string {
	return strings.TrimSpace(buildDate)
}

// IsDevBuild reports whether this binary is a CI per-commit (dev channel) build,
// detected by the injected commit. Stable releases and local builds return false.
func IsDevBuild() bool {
	return GetBuildCommit() != ""
}

// GetPanelVersion returns the version a panel advertises to a managing master
// node and displays in the UI: the plain version for stable builds, or
// "dev+<short commit>" for dev builds. The dev form mirrors the master's
// getPanelUpdateInfo latestVersion so a node on the current dev commit compares
// as up to date instead of always showing "update available".
func GetPanelVersion() string {
	if !IsDevBuild() {
		return GetBaseVersion()
	}
	commit := GetBuildCommit()
	if len(commit) > 8 {
		commit = commit[:8]
	}
	return "dev+" + commit
}

// GetLogLevel returns the current logging level based on environment variables or defaults to Info.
func GetLogLevel() LogLevel {
	if IsDebug() {
		return Debug
	}
	logLevel := os.Getenv("PUI_LOG_LEVEL")
	if logLevel == "" {
		return Info
	}
	return LogLevel(logLevel)
}

// IsDebug returns true if debug mode is enabled via the PUI_DEBUG environment variable.
func IsDebug() bool {
	return os.Getenv("PUI_DEBUG") == "true"
}

// IsSkipHSTS returns true if skipping HSTS mode is enabled via the PUI_SKIP_HSTS environment variable.
func IsSkipHSTS() bool {
	return os.Getenv("PUI_SKIP_HSTS") == "true"
}

func GetPortOverride() (port int, configured bool, err error) {
	value, ok := os.LookupEnv("PUI_PORT")
	if !ok || strings.TrimSpace(value) == "" {
		return 0, false, nil
	}

	port, err = strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, true, fmt.Errorf("parse PUI_PORT: %w", err)
	}
	if port < 1 || port > 65535 {
		return 0, true, fmt.Errorf("PUI_PORT must be between 1 and 65535")
	}

	return port, true, nil
}

// GetBinFolderPath returns the path to the binary folder, defaulting to "bin" if not set via PUI_BIN_FOLDER.
func GetBinFolderPath() string {
	binFolderPath := os.Getenv("PUI_BIN_FOLDER")
	if binFolderPath == "" {
		binFolderPath = "bin"
	}
	return binFolderPath
}

// testStateFolder is the folder GetDBFolderPath redirects to under `go test`.
//
// It is created on first use rather than merely named: callers write files
// straight into this folder (os.WriteFile does not create parent directories),
// so it has to exist before the first write. It is also private to this
// process, so the test binaries of two packages that both touch panel state --
// internal/web/controller and internal/web/service/panel both read and write
// the update status file -- cannot clobber each other when `go test ./...`
// runs them concurrently.
var testStateFolder = sync.OnceValue(func() string {
	dir, err := os.MkdirTemp("", "p-ui-test-state-")
	if err != nil {
		// The temp dir itself is unusable, which the rest of the test run is
		// about to trip over anyway; fall back to it directly so this never
		// hands back a path that does not exist.
		return os.TempDir()
	}
	return dir
})

// GetDBFolderPath returns the panel's persistent state folder. It holds the
// files the panel keeps outside PUI_MAIN_FOLDER so they survive an update (the
// metrics history and the self-update status file).
func GetDBFolderPath() string {
	// A `go test` run has no business reading or writing /etc/p-ui (and on a
	// CI runner cannot): redirect it to a private temp folder, so tests
	// neither depend on nor clobber a real installation's state.
	if testing.Testing() {
		return testStateFolder()
	}
	return "/etc/p-ui"
}

// GetUpdateStatusFilePath returns the path to the panel self-update status
// file update.sh writes on completion. It lives in the persistent state folder,
// outside PUI_MAIN_FOLDER, so it survives an update regardless of what happens
// to that folder.
func GetUpdateStatusFilePath() string {
	return filepath.Join(GetDBFolderPath(), "update-status.json")
}

// GetDBDSN returns the PostgreSQL DSN from PUI_DB_DSN. PostgreSQL is the only
// supported backend, so the panel refuses to start when this is empty.
func GetDBDSN() string {
	return strings.TrimSpace(os.Getenv("PUI_DB_DSN"))
}

// GetEnvFilePath returns the service environment file systemd loads via
// EnvironmentFile on the supported Debian/Ubuntu systems.
func GetEnvFilePath() string {
	return "/etc/default/p-ui"
}

// GetLogFolder returns the log folder from PUI_LOG_FOLDER, or /var/log/p-ui.
func GetLogFolder() string {
	logFolderPath := os.Getenv("PUI_LOG_FOLDER")
	if logFolderPath != "" {
		return logFolderPath
	}
	// A `go test` run has no business writing to /var/log/p-ui (and usually
	// cannot): redirect it to a shared temp folder instead.
	if testing.Testing() {
		return filepath.Join(os.TempDir(), "p-ui-test-log")
	}
	return "/var/log/p-ui"
}
