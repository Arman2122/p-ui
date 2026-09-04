package mtproto

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// scriptedDraw returns the given ports in order, repeating the last one forever.
func scriptedDraw(ports ...int) func() (int, error) {
	i := 0
	return func() (int, error) {
		p := ports[i]
		if i < len(ports)-1 {
			i++
		}
		return p, nil
	}
}

func webBackendManaged(t *testing.T, draw func() (int, error)) (*Manager, Instance) {
	t.Helper()
	t.Setenv("PUI_BIN_FOLDER", t.TempDir())
	inst := webBackendInstance()
	inst.Id = 3
	inst.Tag = "inbound-3"
	inst.WebBackendEnable = true
	inst.WebBackendPort = 2398
	return &Manager{procs: map[int]*managed{}, swept: true, drawPort: draw}, inst
}

// The api port is drawn in memory, so no claim check can see it against the
// persisted WEB backend port -- and mtg loses an extra-listener bind silently.
func TestAPIPortIsDrawnClearOfTheWebBackendPort(t *testing.T) {
	// Both directions: the rejection is inequality, not ordering. With the clear
	// port only ever above the avoided one, "port > avoid" would pass too, and
	// that draws every api port into a range the backend port sits below.
	cases := map[string]struct {
		draws []int
		want  int
	}{
		"clear port above the avoided one": {draws: []int{2398, 2398, 2400}, want: 2400},
		"clear port below it":              {draws: []int{2398, 2398, 2396}, want: 2396},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			mgr, inst := webBackendManaged(t, scriptedDraw(tc.draws...))

			// No mtg binary in the temp bin folder, so the start fails after the
			// config is written -- and the written config is the artifact under test.
			_ = mgr.ensureLocked(inst)

			raw, err := os.ReadFile(configPathForID(inst.Id))
			if err != nil {
				t.Fatalf("config not written: %v", err)
			}
			cfg := string(raw)
			if want := fmt.Sprintf("api-bind-to = \"127.0.0.1:%d\"\n", tc.want); !strings.Contains(cfg, want) {
				t.Fatalf("api port must skip the WEB backend port and take the next draw; want %q in:\n%s", want, cfg)
			}
			if strings.Count(cfg, "127.0.0.1:2398") != 1 {
				t.Fatalf("127.0.0.1:2398 must belong to the [[listener]] alone, config:\n%s", cfg)
			}
		})
	}
}

func TestAPIPortDrawGivesUpWithTheCollidingPortNamed(t *testing.T) {
	mgr, inst := webBackendManaged(t, scriptedDraw(2398))

	err := mgr.ensureLocked(inst)
	want := fmt.Sprintf("mtproto: no api port clear of the WEB backend port 2398 after %d attempts", apiPortDrawAttempts)
	if err == nil || err.Error() != want {
		t.Fatalf("want error %q, got %v", want, err)
	}
	if _, statErr := os.Stat(configPathForID(inst.Id)); !os.IsNotExist(statErr) {
		t.Fatalf("no config may be written when no api port could be drawn (stat err=%v)", statErr)
	}
}

// A disabled backend reserves nothing, so the api draw must not re-roll on the
// stale port value the settings may still carry.
func TestADisabledWebBackendDoesNotConstrainTheAPIPort(t *testing.T) {
	mgr, inst := webBackendManaged(t, scriptedDraw(2398))
	inst.WebBackendEnable = false

	_ = mgr.ensureLocked(inst)

	raw, err := os.ReadFile(configPathForID(inst.Id))
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if want := "api-bind-to = \"127.0.0.1:2398\"\n"; !strings.Contains(string(raw), want) {
		t.Fatalf("want %q in:\n%s", want, raw)
	}
}
