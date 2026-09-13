package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/web/runtime"
)

// CoreVersionsView is a core's release picture: the build on disk and, when the
// core can install one, the channel's list.
type CoreVersionsView struct {
	Installed  string   `json:"installed,omitempty" example:"26.9.9"`
	Available  []string `json:"available" example:"[\"v26.9.9\",\"v26.7.28\"]"`
	Manageable bool     `json:"manageable" example:"true"`
	// Error is why Installed or Available is missing; a missing binary is one.
	Error string `json:"error,omitempty" example:"core: daemon not installed"`
}

// releaseListTTL bounds how often a core's release list hits its channel; the
// list changes a few times a month and GitHub rate-limits anonymous callers.
const releaseListTTL = 15 * time.Minute

type cachedReleases struct {
	versions  []string
	fetchedAt time.Time
}

var (
	releaseCacheMu sync.Mutex
	releaseCache   = map[string]cachedReleases{}
)

// CoreVersions is GET /cores/:id/versions. A stale list is served over a failed
// refresh: the alternative is a picker that goes blank whenever GitHub blinks.
func CoreVersions(ctx context.Context, coreID string) (CoreVersionsView, error) {
	bound, err := boundCore(coreID)
	if err != nil {
		return CoreVersionsView{}, err
	}
	view := CoreVersionsView{Available: []string{}, Manageable: bound.Versions != nil}
	if bound.Version == nil {
		return view, fmt.Errorf("%w: %q", ErrCoreNotVersioned, coreID)
	}
	if installed, err := bound.Version.Installed(ctx); err != nil {
		view.Error = err.Error()
	} else {
		view.Installed = installed
	}
	if bound.Versions == nil {
		return view, nil
	}
	versions, err := releasesCached(ctx, coreID, bound.Versions)
	if err != nil {
		if view.Error == "" {
			view.Error = err.Error()
		}
		return view, nil
	}
	view.Available = versions
	return view, nil
}

func releasesCached(ctx context.Context, coreID string, versions core.VersionManager) ([]string, error) {
	releaseCacheMu.Lock()
	cached, ok := releaseCache[coreID]
	releaseCacheMu.Unlock()
	if ok && time.Since(cached.fetchedAt) <= releaseListTTL {
		return cached.versions, nil
	}
	fresh, err := versions.Available(ctx)
	if err != nil {
		if ok {
			logger.Warning("core", coreID, "release list: serving the stale one:", err)
			return cached.versions, nil
		}
		return nil, err
	}
	releaseCacheMu.Lock()
	releaseCache[coreID] = cachedReleases{versions: fresh, fetchedAt: time.Now()}
	releaseCacheMu.Unlock()
	return fresh, nil
}

// InstallCoreVersion is POST /cores/:id/install/:version: the binary lands, the
// daemon keeps running the old one until a restart, which stays the operator's.
func InstallCoreVersion(ctx context.Context, coreID, version string) error {
	bound, err := boundCore(coreID)
	if err != nil {
		return err
	}
	if bound.Versions == nil {
		return fmt.Errorf("%w: %q", ErrCoreNotVersioned, coreID)
	}
	return bound.Versions.Install(ctx, version)
}

// CoreLogs is GET /cores/:id/logs: the daemon's last n lines, one instance of a
// per-inbound core or the whole of a single-process one (instance 0).
func CoreLogs(ctx context.Context, coreID string, instanceID, n int) ([]string, error) {
	bound, err := boundCore(coreID)
	if err != nil {
		return nil, err
	}
	if bound.Logs == nil {
		return nil, fmt.Errorf("%w: %q", ErrCoreNoLogs, coreID)
	}
	lines, err := bound.Logs.TailLog(ctx, instanceID, n)
	if err != nil {
		return nil, err
	}
	if lines == nil {
		lines = []string{}
	}
	return lines, nil
}

// boundCore resolves a core id the way RestartCore does, with the same errors.
func boundCore(coreID string) (*core.Bound, error) {
	manager := runtime.GetManager()
	if manager == nil {
		return nil, ErrCoreUnknown
	}
	bound, found := coreByID(manager.Cores(), coreID)
	if !found {
		return nil, fmt.Errorf("%w: %q", ErrCoreUnknown, coreID)
	}
	return bound, nil
}

var ErrCoreNoLogs = errors.New("core: this core keeps no log the panel can read")
