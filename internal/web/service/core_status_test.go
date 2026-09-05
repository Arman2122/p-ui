package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
)

// statusBase is the least a registered core can be.
type statusBase struct {
	id    core.Kind
	kinds []core.Kind
}

func (c statusBase) Kinds() []core.Kind { return c.kinds }
func (c statusBase) Describe() core.Descriptor {
	return core.Descriptor{ID: c.id, TitleKey: "cores." + string(c.id) + ".title", Caps: core.Capabilities{
		UserHotAdd: core.No(), PerUserStats: core.No(), QuotaPushdown: core.No(), OnlineUsers: core.No(), ShareLink: core.No(),
	}}
}
func (c statusBase) Preflight(context.Context) error { return nil }

// healthCore reports whatever health a case gives it.
type healthCore struct {
	statusBase
	health    core.Health
	healthErr error
}

func (c *healthCore) Health(context.Context) (core.Health, error) { return c.health, c.healthErr }

type versionedCore struct {
	*healthCore
	installed string
	instErr   error
}

func (c versionedCore) Installed(context.Context) (string, error) { return c.installed, c.instErr }

type managedCore struct {
	versionedCore
	available *[]string
	availErr  *error
}

func (c managedCore) Available(context.Context) ([]string, error) { return *c.available, *c.availErr }
func (c managedCore) Install(context.Context, string) error       { return nil }

// loggedCore reads a log and reports no health at all.
type loggedCore struct{ statusBase }

func (c loggedCore) TailLog(context.Context, int, int) ([]string, error) { return []string{"x"}, nil }

/*
The status view is the one translation of core.Health the page sees, so each
shape is pinned: a per-inbound core's overall state derives from its instances,
a single-process core fills the top, a core without health says unknown rather
than guessing, and the version block tells reported and manageable apart.
*/
func TestCoreStatusViewTranslatesEachShape(t *testing.T) {
	perInbound := versionedCore{healthCore: &healthCore{statusBase: statusBase{id: "daemon", kinds: []core.Kind{"d"}}, health: core.Health{Instances: []core.InstanceHealth{
		{InstanceID: 1, Tag: "d-1", State: core.HealthRunning, Ready: true, PID: 10, SinceUnixMilli: 5},
		{InstanceID: 2, Tag: "d-2", State: core.HealthFailed, Detail: "signal: killed"},
	}}}, installed: "1.2.4"}
	available, availErr := []string{"v2", "v1"}, error(nil)
	single := managedCore{versionedCore: versionedCore{healthCore: &healthCore{statusBase: statusBase{id: "single", kinds: []core.Kind{"s"}}, health: core.Health{State: core.HealthRunning, Detail: "the API port does not answer", PID: 7}}, installed: "1"}, available: &available, availErr: &availErr}
	mute := loggedCore{statusBase{id: "mute", kinds: []core.Kind{"m"}}}
	broken := versionedCore{healthCore: &healthCore{statusBase: statusBase{id: "broken", kinds: []core.Kind{"b"}}, healthErr: errors.New("dbus: connection closed")}, instErr: core.ErrNotInstalled}

	reg := core.NewRegistry()
	for _, c := range []core.Core{perInbound, single, mute, broken} {
		if err := reg.Register(c); err != nil {
			t.Fatal(err)
		}
	}
	views := coreStatusViews(context.Background(), reg)
	byID := map[string]CoreStatusView{}
	for _, v := range views {
		byID[v.ID] = v
	}
	if len(byID) != 4 {
		t.Fatalf("views = %d, want 4", len(views))
	}

	d := byID["daemon"]
	if d.State != "degraded" || d.Ready || len(d.Instances) != 2 || d.Instances[1].Detail != "signal: killed" || d.Instances[0].PID != 10 || d.Instances[0].SinceUnixMilli != 5 {
		t.Fatalf("per-inbound view = %+v; want degraded, not ready, both instances carried", d)
	}
	if !d.Version.Reported || d.Version.Manageable || d.Version.Installed != "1.2.4" || d.Logs {
		t.Fatalf("per-inbound version/logs = %+v %v; want reported, not manageable, no logs", d.Version, d.Logs)
	}

	s := byID["single"]
	if s.State != "running" || s.Ready || s.Detail != "the API port does not answer" || s.PID != 7 || len(s.Instances) != 0 {
		t.Fatalf("single-process view = %+v; want the top level filled and no instances", s)
	}
	if !s.Version.Manageable || s.Version.Installed != "1" {
		t.Fatalf("single version = %+v; want manageable", s.Version)
	}

	m := byID["mute"]
	if m.State != "unknown" || m.Version.Reported || m.Version.Manageable || !m.Logs {
		t.Fatalf("mute view = %+v; want unknown health, no version, logs offered", m)
	}

	b := byID["broken"]
	if b.State != "unknown" || b.HealthError != "dbus: connection closed" || b.Version.Error != core.ErrNotInstalled.Error() || b.Version.Installed != "" {
		t.Fatalf("broken view = %+v; want the health error and the not-installed state carried, not swallowed", b)
	}
	for _, v := range views {
		if v.Instances == nil {
			t.Fatalf("%s: Instances is nil; the JSON must be [] so the page can map it", v.ID)
		}
	}
}

// A stale list outlives a failed refresh: the picker must not go blank because
// GitHub blinked, and a first failure with nothing cached is still an error.
func TestReleaseListServesTheStaleCopyOverAFailedRefresh(t *testing.T) {
	releaseCacheMu.Lock()
	delete(releaseCache, "t")
	releaseCacheMu.Unlock()
	t.Cleanup(func() {
		releaseCacheMu.Lock()
		delete(releaseCache, "t")
		releaseCacheMu.Unlock()
	})
	available, availErr := []string(nil), errors.New("503")
	m := managedCore{versionedCore: versionedCore{healthCore: &healthCore{statusBase: statusBase{id: "t", kinds: []core.Kind{"t"}}}}, available: &available, availErr: &availErr}
	if _, err := releasesCached(context.Background(), "t", m); err == nil {
		t.Fatal("a failed first fetch with nothing cached must be an error")
	}
	available, availErr = []string{"v2", "v1"}, nil
	got, err := releasesCached(context.Background(), "t", m)
	if err != nil || len(got) != 2 {
		t.Fatalf("fresh list = %v, %v", got, err)
	}
	releaseCacheMu.Lock()
	releaseCache["t"] = cachedReleases{versions: got, fetchedAt: time.Now().Add(-2 * releaseListTTL)}
	releaseCacheMu.Unlock()
	availErr = errors.New("503 again")
	got, err = releasesCached(context.Background(), "t", m)
	if err != nil || len(got) != 2 {
		t.Fatalf("stale list = %v, %v; want the cached two served over the failure", got, err)
	}
}
