package cores

import "testing"

// TestDefaultRegistryIsCoherent guards the wiring, not any one core: a lying
// descriptor, an unresolvable kind, or a dropped Register line all surface here.
func TestDefaultRegistryIsCoherent(t *testing.T) {
	reg, err := Default()
	if err != nil {
		t.Fatalf("the default registry must build: %v", err)
	}
	bound := reg.Cores()
	if len(bound) == 0 {
		t.Fatal("no core is registered; a dropped Register line leaves every inbound unserveable")
	}
	for _, b := range bound {
		d := b.Core.Describe()
		for _, problem := range b.DeclaredMatchesImplemented() {
			t.Errorf("%s declares a capability it does not implement, or vice versa: %s", d.ID, problem)
		}
		if d.TitleKey == "" {
			t.Errorf("%s has no TitleKey; the UI would render it nameless", d.ID)
		}
		for _, kind := range b.Core.Kinds() {
			got, ok := reg.For(kind)
			if !ok {
				t.Errorf("kind %q is registered by %s but does not resolve", kind, d.ID)
				continue
			}
			if got != b {
				t.Errorf("kind %q resolves to %s, not the %s that claimed it", kind, got.Core.Describe().ID, d.ID)
			}
		}
	}
}
