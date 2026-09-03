package supervisor

import (
	"bytes"
	"strings"
	"testing"
	"time"

	godbus "github.com/godbus/dbus/v5"
)

func sampleSpec() Spec {
	return Spec{
		Name:               UnitName("mtproto", 7),
		Description:        "mtg for inbound 7",
		Exec:               []string{"/usr/local/p-ui/bin/mtg-linux-amd64", "run", "/etc/p-ui/mtg-7.toml"},
		Env:                []string{"PUI_CONFIG_HASH=abc"},
		WorkingDir:         "/usr/local/p-ui",
		User:               "p-ui",
		Capabilities:       []string{"CAP_NET_BIND_SERVICE", "CAP_NET_ADMIN"},
		Restart:            RestartOnFailure,
		RestartAfter:       5 * time.Second,
		StartLimitInterval: 3 * time.Minute,
		StartLimitBurst:    10,
		StopTimeout:        7 * time.Second,
	}
}

// Ensure decides "leave it" against "replace it" by this hash, so a field that
// changes the running process must move it and one that does not must not.
func TestSpecHashTracksWhatRuns(t *testing.T) {
	base := sampleSpec()
	same := sampleSpec()
	same.Output = &bytes.Buffer{}
	same.OnExit = func(error) {}
	if base.Hash() != same.Hash() {
		t.Fatal("Output and OnExit changed the hash; they are the panel's side, not the process")
	}
	for name, mutate := range map[string]func(*Spec){
		"exec":       func(s *Spec) { s.Exec = append(s.Exec, "--verbose") },
		"env":        func(s *Spec) { s.Env = []string{"PUI_CONFIG_HASH=def"} },
		"user":       func(s *Spec) { s.User = "root" },
		"caps":       func(s *Spec) { s.Capabilities = nil },
		"restartsec": func(s *Spec) { s.RestartAfter = time.Second },
		"stop":       func(s *Spec) { s.StopTimeout = time.Second },
	} {
		changed := sampleSpec()
		mutate(&changed)
		if changed.Hash() == base.Hash() {
			t.Errorf("%s: changed the process and not the hash", name)
		}
	}
}

func TestSpecValidateRefusesWhatNoBackendRuns(t *testing.T) {
	for name, tt := range map[string]struct {
		mutate func(*Spec)
		want   string
	}{
		"foreign name":    {func(s *Spec) { s.Name = "nginx" }, "does not start with p-ui-"},
		"unit characters": {func(s *Spec) { s.Name = "p-ui-a b" }, "not a valid unit name"},
		"no command":      {func(s *Spec) { s.Exec = nil }, "has no command"},
		"bad capability":  {func(s *Spec) { s.Capabilities = []string{"CAP_NET_ADMN"} }, "unknown capability"},
		"bad policy":      {func(s *Spec) { s.Restart = "sometimes" }, "unknown restart policy"},
	} {
		t.Run(name, func(t *testing.T) {
			s := sampleSpec()
			tt.mutate(&s)
			err := s.Validate()
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Validate() = %v, want an error containing %q", err, tt.want)
			}
		})
	}
	if err := sampleSpec().Validate(); err != nil {
		t.Fatalf("a well-formed spec was refused: %v", err)
	}
}

// The property list is what systemd receives; a wrong name or D-Bus type is
// rejected by the manager at runtime with nothing more than "invalid argument".
func TestTransientPropertiesSpeakSystemd(t *testing.T) {
	spec := sampleSpec()
	props, err := transientProperties(spec)
	if err != nil {
		t.Fatalf("transientProperties: %v", err)
	}
	got := map[string]godbus.Variant{}
	for _, p := range props {
		got[p.Name] = p.Value
	}
	want := map[string]any{
		"Restart":                "on-failure",
		"KillSignal":             int32(15),
		"TimeoutStopUSec":        uint64(7_000_000),
		"RestartUSec":            uint64(5_000_000),
		"StartLimitIntervalUSec": uint64(180_000_000),
		"StartLimitBurst":        uint32(10),
		"User":                   "p-ui",
		"WorkingDirectory":       "/usr/local/p-ui",
		"AmbientCapabilities":    uint64(1<<10 | 1<<12),
		"CollectMode":            "inactive",
		"Slice":                  DefaultSlice,
		"Type":                   "simple",
	}
	for name, value := range want {
		v, ok := got[name]
		if !ok {
			t.Errorf("%s: missing", name)
			continue
		}
		if v.Value() != value {
			t.Errorf("%s = %#v (%T), want %#v (%T)", name, v.Value(), v.Value(), value, value)
		}
	}
	desc, _ := got["Description"].Value().(string)
	if hashFromDescription(desc) != spec.Hash() {
		t.Fatalf("Description %q does not carry the spec hash %s", desc, spec.Hash())
	}
	env, _ := got["Environment"].Value().([]string)
	if len(env) != 1 || env[0] != "PUI_CONFIG_HASH=abc" {
		t.Fatalf("Environment = %v", env)
	}
	if _, ok := got["ExecStart"]; !ok {
		t.Fatal("ExecStart missing")
	}
}
