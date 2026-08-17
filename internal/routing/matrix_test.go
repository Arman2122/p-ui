package routing

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

/*
The product's promise, stated as one assertion: any protocol in, any protocol out.

Each cell is reachable through some existing test, but none of them says the
matrix is the contract — so a core added later can lose a cell without any test
going red, and the loss shows up as an operator's rule that silently routes
nothing. This is the guard for that: a new ingress selector or exit kind belongs
in this table on the commit that introduces it.

The two realisations are deliberately different and both must keep working. An
L7 ingress is its own router, so its rule is an Xray rule on its own tag. An L3
ingress has no domain or port vocabulary in the FIB, so it is fronted into Xray
first and the rule is rewritten onto the front's tag; the destination then
decides again, an outbound tag directly or a marked socket into an exit's table.
*/
func TestEveryIngressCanReachEveryExit(t *testing.T) {
	const frontedInbound = 7

	for _, ingress := range []struct {
		name    string
		subject Subject
		// fronted says this ingress needs kernel state to be routable at all.
		fronted bool
	}{
		{"an L7 ingress that routes itself", internalSubject(1, "vless-in"), false},
		{"an L3 ingress that must be fronted", deviceSubject(frontedInbound, "wg-home", "pwg7"), true},
	} {
		for _, exit := range []struct {
			name string
			dest Dest
			// A daemon-sourced device exit translates its own source; anything the
			// panel would have to NAT for is refused, which is its own test.
			exits []ResolvedExit
		}{
			{"an Xray outbound", Dest{Kind: DestOutbound, Tag: "de"}, nil},
			{
				"another core's exit",
				Dest{Kind: DestExit, ExitID: 3},
				[]ResolvedExit{{
					ID: 3, Label: "wg uplink", Kind: core.ExitDevice,
					Handle: core.ExitHandle{Device: "pux3", Source: core.SourceOwnerDaemon},
				}},
			},
		} {
			t.Run(ingress.name+" → "+exit.name, func(t *testing.T) {
				fronts := map[int]int{}
				if ingress.fronted {
					fronts[frontedInbound] = 1
				}
				got := Plan(Input{
					Subjects:   []Subject{ingress.subject},
					Rules:      []Rule{{ID: 1, Enable: true, Scope: ScopeAll, Dest: exit.dest}},
					Exits:      exit.exits,
					Blackhole:  "blocked",
					Direct:     "direct",
					FrontIDFor: frontIDs(fronts),
				})

				if refusals := got.Refusals(); len(refusals) != 0 {
					t.Fatalf("this pair must be routable, but the compile refused it: %v", refusals)
				}
				if len(got.XrayRules) == 0 {
					t.Fatal("no rule was emitted, so the operator's intent reaches nothing")
				}
				for _, diag := range got.Diags {
					if diag.Severity == SeverityInert {
						t.Fatalf("the rule compiled to something inert: %+v", diag)
					}
				}
				// An L3 ingress is only routable because the compile makes its front;
				// without that kernel row the rewritten tag names a device nothing owns.
				if ingress.fronted && len(got.Kernel) == 0 {
					t.Fatal("a fronted ingress emitted no kernel state to be fronted by")
				}
			})
		}
	}
}
