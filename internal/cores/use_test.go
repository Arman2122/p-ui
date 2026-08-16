package cores

import (
	"context"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

/*
The facade must answer from the registry the panel wired, not its fallback.

Every facade helper used to read a second, never-started build of the adapters,
so it could disagree with the running cores about anything stateful. Use is what
the production wiring calls, and this pins that a wired answer wins: the probe
serves a kind no real core claims, so only the wired registry can know it.
*/
func TestUseRepointsTheFacade(t *testing.T) {
	wired := core.NewRegistry()
	if err := wired.Register(&facadeProbeCore{}); err != nil {
		t.Fatalf("register the probe core: %v", err)
	}

	Use(wired)
	t.Cleanup(func() { wiredRegistry.Store(nil) })

	if got := ClientCredentials("facade-probe"); len(got) != 1 || got[0] != core.CredPassword {
		t.Fatalf("ClientCredentials(facade-probe) = %v; the wired registry declares [password], so the facade answered from its fallback", got)
	}
	authority, ok := ClientCredentialAuthority("facade-probe")
	if !ok {
		t.Fatal("the wired kind is invisible; the facade is still reading its fallback build")
	}
	if identity := authority.ClientIdentity("facade-probe"); identity != "email" {
		t.Fatalf("ClientIdentity = %q, want the wired core's answer", identity)
	}

	// Use(nil) must not blank the facade: a caller with nothing to wire leaves
	// whatever is wired alone rather than downgrading it to the fallback.
	Use(nil)
	if _, ok := ClientCredentialAuthority("facade-probe"); !ok {
		t.Fatal("Use(nil) forgot the wired registry")
	}
}

// The fallback still serves a process that never wires: the CLI, codegen, and
// any test that calls a helper directly.
func TestTheFallbackStillAnswersWhenNothingIsWired(t *testing.T) {
	wiredRegistry.Store(nil)
	if got := ClientCredentials("vless"); len(got) == 0 {
		t.Fatal("with nothing wired, the fallback no longer answers for vless")
	}
	if len(Kinds()) == 0 {
		t.Fatal("Kinds() answers nothing with no wired registry")
	}
}

// facadeProbeCore serves a kind no real core claims.
type facadeProbeCore struct{}

func (f *facadeProbeCore) Describe() core.Descriptor {
	return core.Descriptor{
		ID: "facade-probe", TitleKey: "cores.facadeprobe.title",
		Caps: core.Capabilities{
			UserHotAdd: core.No(), PerUserStats: core.No(), QuotaPushdown: core.No(),
			OnlineUsers: core.No(), ShareLink: core.No(),
		},
	}
}

func (f *facadeProbeCore) Kinds() []core.Kind { return []core.Kind{"facade-probe"} }

func (f *facadeProbeCore) Preflight(context.Context) error { return nil }

func (f *facadeProbeCore) ClientCredentials(core.Kind) []string {
	return []string{core.CredPassword}
}

func (f *facadeProbeCore) MintClientCredentials(core.Kind, string, map[string]string) (map[string]string, error) {
	return map[string]string{}, nil
}

func (f *facadeProbeCore) ValidateClient(core.Kind, string, string, map[string]string) error {
	return nil
}

func (f *facadeProbeCore) ClientIdentity(core.Kind) string { return "email" }
