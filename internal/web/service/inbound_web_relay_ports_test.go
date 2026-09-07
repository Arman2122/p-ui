package service

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/database/model"
)

// The relay needs three loopback sockets of its own beside the proxy: the
// dd/plain backend it dials, its bind address for the front proxy, and its
// stats socket. Turning the relay on allocates all three, distinct from each
// other and from the bridge; turning it off drops the two that are the relay's
// alone and keeps the backend only while it is wanted on its own.
func TestNormalizeWebPortsForTheRelay(t *testing.T) {
	s := webBackendService(t)

	t.Run("the relay allocates three distinct ports", func(t *testing.T) {
		ib := &model.Inbound{Protocol: model.MTProto, Settings: `{"webEnable":true,"routeXrayPort":51000}`}
		if err := s.normalizeWebBackendPort(ib, ""); err != nil {
			t.Fatal(err)
		}
		seen := map[int]string{51000: "routeXrayPort"}
		for _, key := range webPortKeys {
			port := webPortOf(t, ib.Settings, key)
			if port <= 0 {
				t.Fatalf("%s must be allocated when the relay is on, got %d\n%s", key, port, ib.Settings)
			}
			if other, dup := seen[port]; dup {
				t.Fatalf("%s and %s share port %d", key, other, port)
			}
			seen[port] = key
		}
	})

	t.Run("stored ports survive an edit and a disabled relay keeps only the backend", func(t *testing.T) {
		old := `{"webEnable":true,"webBackendEnable":true,"webBackendPort":51101,"webBindPort":51102,"webStatsPort":51103}`
		ib := &model.Inbound{Protocol: model.MTProto, Settings: old}
		if err := s.normalizeWebBackendPort(ib, old); err != nil {
			t.Fatal(err)
		}
		for key, want := range map[string]int{"webBackendPort": 51101, "webBindPort": 51102, "webStatsPort": 51103} {
			if got := webPortOf(t, ib.Settings, key); got != want {
				t.Fatalf("%s moved from %d to %d on a save that changed nothing", key, want, got)
			}
		}

		off := &model.Inbound{Protocol: model.MTProto, Settings: `{"webEnable":false,"webBackendEnable":true,"webBackendPort":51101,"webBindPort":51102,"webStatsPort":51103}`}
		if err := s.normalizeWebBackendPort(off, old); err != nil {
			t.Fatal(err)
		}
		if got := webPortOf(t, off.Settings, "webBackendPort"); got != 51101 {
			t.Fatalf("the backend is still wanted on its own and must keep its port, got %d", got)
		}
		for _, key := range []string{"webBindPort", "webStatsPort"} {
			if got := webPortOf(t, off.Settings, key); got != 0 {
				t.Fatalf("%s belongs to the relay alone and must go with it, got %d", key, got)
			}
		}
	})
}

func webPortOf(t *testing.T, settings, key string) int {
	t.Helper()
	return parseSettingsPort(settings, key)
}
