package routing

import (
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

// socksExit is the exit shape that is realisable today, so these cases are
// ordinary configurations rather than contrived ones.
func socksExit(id int) ResolvedExit {
	return ResolvedExit{
		ID: id, Label: "bridge", Kind: core.ExitSocksPort,
		Handle: core.ExitHandle{SocksPort: 2398, Source: core.SourceOwnerDaemon},
	}
}

func deviceExit(id int) ResolvedExit {
	return ResolvedExit{
		ID: id, Label: "uplink", Kind: core.ExitDevice,
		Handle: core.ExitHandle{Device: "pux4", Source: core.SourceOwnerDaemon},
	}
}

func outboundTags(t *testing.T, compiled Compiled) []string {
	t.Helper()
	tags := make([]string, 0, len(compiled.Outbounds))
	for _, raw := range compiled.Outbounds {
		tag, _ := decode(t, raw)["tag"].(string)
		tags = append(tags, tag)
	}
	return tags
}

/*
An exit's outbound is named after the exit. Emitting it once per (rule, subject)
made two rules on one exit — or one rule naming two inbounds — produce two
outbounds carrying the same tag, and xray-core's outbound manager answers a
duplicate tag with "existing tag found", which fails core.New and takes every
inbound of every protocol down with it.

Each case below is a first-class use of the feature, not an edge.
*/
func TestOneExitYieldsOneOutbound(t *testing.T) {
	tests := []struct {
		name     string
		subjects []Subject
		rules    []Rule
		exits    []ResolvedExit
	}{
		{
			name:     "one rule naming two inbounds",
			subjects: []Subject{internalSubject(1, "a-in"), internalSubject(2, "b-in")},
			rules: []Rule{
				{ID: 1, Enable: true, Scope: ScopeSelected, IngressIDs: []int{1, 2}, Dest: Dest{Kind: DestExit, ExitID: 4}},
			},
			exits: []ResolvedExit{socksExit(4)},
		},
		{
			name:     "two rules on one inbound, same exit",
			subjects: []Subject{internalSubject(1, "a-in")},
			rules: []Rule{
				{ID: 1, SortIndex: 0, Enable: true, Scope: ScopeSelected, IngressIDs: []int{1}, Dest: Dest{Kind: DestExit, ExitID: 4}},
				{ID: 2, SortIndex: 1, Enable: true, Scope: ScopeSelected, IngressIDs: []int{1}, Dest: Dest{Kind: DestExit, ExitID: 4}},
			},
			exits: []ResolvedExit{socksExit(4)},
		},
		{
			name:     "an all-scope rule over two routable inbounds",
			subjects: []Subject{internalSubject(1, "a-in"), internalSubject(2, "b-in")},
			rules: []Rule{
				{ID: 1, Enable: true, Scope: ScopeAll, Dest: Dest{Kind: DestExit, ExitID: 4}},
			},
			exits: []ResolvedExit{socksExit(4)},
		},
		{
			name:     "a device exit, whose outbound is the marked one",
			subjects: []Subject{internalSubject(1, "a-in"), internalSubject(2, "b-in")},
			rules: []Rule{
				{ID: 1, Enable: true, Scope: ScopeSelected, IngressIDs: []int{1, 2}, Dest: Dest{Kind: DestExit, ExitID: 4}},
			},
			exits: []ResolvedExit{deviceExit(4)},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Plan(Input{
				Subjects: tc.subjects, Rules: tc.rules, Exits: tc.exits,
				Blackhole: "blocked", Direct: "direct", FrontIDFor: frontIDs(nil),
			})

			if len(got.Refusals()) != 0 {
				t.Fatalf("the exit is realisable, got refusals %+v", got.Refusals())
			}
			tags := outboundTags(t, got)
			if len(tags) != 1 || tags[0] != "pex4" {
				t.Fatalf("outbound tags = %v, want exactly [pex4]: xray refuses to load a config with two handlers on one tag", tags)
			}
			if len(got.XrayRules) != len(tc.subjects)*len(tc.rules) {
				t.Fatalf("got %d xray rules, want one per (rule, subject): dedupe must not drop a rule",
					len(got.XrayRules))
			}
			for _, raw := range got.XrayRules {
				if decode(t, raw)["outboundTag"] != "pex4" {
					t.Fatal("every rule must still target the outbound the compile synthesized")
				}
			}
		})
	}
}

// Two different exits are two different tags, so both are still emitted.
func TestTwoExitsKeepTheirOwnOutbounds(t *testing.T) {
	got := Plan(Input{
		Subjects: []Subject{internalSubject(1, "a-in")},
		Rules: []Rule{
			{ID: 1, SortIndex: 0, Enable: true, Scope: ScopeSelected, IngressIDs: []int{1}, Dest: Dest{Kind: DestExit, ExitID: 4}},
			{ID: 2, SortIndex: 1, Enable: true, Scope: ScopeSelected, IngressIDs: []int{1}, Dest: Dest{Kind: DestExit, ExitID: 5}},
		},
		Exits:     []ResolvedExit{socksExit(4), socksExit(5)},
		Blackhole: "blocked", Direct: "direct", FrontIDFor: frontIDs(nil),
	})

	tags := outboundTags(t, got)
	if len(tags) != 2 || tags[0] != "pex4" || tags[1] != "pex5" {
		t.Fatalf("outbound tags = %v, want [pex4 pex5]", tags)
	}
}
