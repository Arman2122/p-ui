package routing

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

func internalSubject(id int, tag string) Subject {
	return Subject{InboundID: id, Tag: tag, Handle: core.IngressHandle{Tag: tag}}
}

func deviceSubject(id int, tag, device string) Subject {
	return Subject{InboundID: id, Tag: tag, Handle: core.IngressHandle{Device: device}}
}

// frontIDs hands out one front per ingress, which is what the service layer does
// through an upsert on egresses.ingress_inbound_id.
func frontIDs(m map[int]int) func(int) (int, bool) {
	return func(inboundID int) (int, bool) {
		id, ok := m[inboundID]
		return id, ok
	}
}

func baseInput(subjects []Subject, rules []Rule, fronts map[int]int) Input {
	return Input{
		Rules: rules, Subjects: subjects, Blackhole: "blocked", Direct: "direct",
		FrontIDFor: frontIDs(fronts),
	}
}

func decode(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("emitted rule is not an object: %v (%s)", err, raw)
	}
	return out
}

func inboundTags(t *testing.T, raw json.RawMessage) []string {
	t.Helper()
	tags, ok := decode(t, raw)["inboundTag"].([]any)
	if !ok {
		t.Fatalf("emitted rule carries no inboundTag: %s", raw)
	}
	out := make([]string, 0, len(tags))
	for _, tag := range tags {
		out = append(out, tag.(string))
	}
	return out
}

/*
An L7 core is its own router, so naming its inbound costs nothing: no device, no
table, no front. A design that fronts everything pays gVisor for every flow.
*/
func TestInternalIngressEmitsNoKernelState(t *testing.T) {
	got := Plan(baseInput(
		[]Subject{internalSubject(1, "vless-in")},
		[]Rule{{ID: 1, Enable: true, Scope: ScopeSelected, IngressIDs: []int{1},
			Dest: Dest{Kind: DestOutbound, Tag: "warp"}}},
		nil,
	))

	if len(got.Kernel) != 0 || len(got.Fronts) != 0 {
		t.Fatalf("an internal ingress needs no kernel state, got %d kernel and %d fronts", len(got.Kernel), len(got.Fronts))
	}
	if len(got.XrayRules) != 1 {
		t.Fatalf("want exactly one rule, got %d", len(got.XrayRules))
	}
	if tags := inboundTags(t, got.XrayRules[0]); len(tags) != 1 || tags[0] != "vless-in" {
		t.Fatalf("inboundTag = %v, want [vless-in]", tags)
	}
	if got.NeedsRestart {
		t.Error("only a rules-array change: this is hot-appliable and must not arm a restart")
	}
}

/*
The bug this whole feature exists for: a wgkernel inbound never becomes an Xray
inbound, so a rule naming its tag was accepted and never matched. Fronting it is
what gives the router something it can see.
*/
func TestDeviceIngressIsFrontedAndTagRewritten(t *testing.T) {
	got := Plan(baseInput(
		[]Subject{deviceSubject(7, "wg-home", "pwg7")},
		[]Rule{
			{ID: 1, SortIndex: 0, Enable: true, Scope: ScopeSelected, IngressIDs: []int{7},
				Dest: Dest{Kind: DestOutbound, Tag: "de"}},
			{ID: 2, SortIndex: 1, Enable: true, Scope: ScopeSelected, IngressIDs: []int{7},
				Dest: Dest{Kind: DestBlock}},
		},
		map[int]int{7: 1},
	))

	if len(got.Kernel) != 1 {
		t.Fatalf("want one kernel row, got %d", len(got.Kernel))
	}
	if got.Kernel[0].ID != 1 || got.Kernel[0].Type != "xray-tun" ||
		len(got.Kernel[0].Ingress) != 1 || got.Kernel[0].Ingress[0] != "pwg7" {
		t.Fatalf("kernel row = %+v, want id 1 xray-tun selecting pwg7", got.Kernel[0])
	}
	if len(got.XrayRules) != 3 {
		t.Fatalf("want the guard plus two rules, got %d", len(got.XrayRules))
	}
	for i, raw := range got.XrayRules {
		if tags := inboundTags(t, raw); len(tags) != 1 || tags[0] != "peg1" {
			t.Errorf("rule %d inboundTag = %v, want [peg1]", i, tags)
		}
	}
	if guard := decode(t, got.XrayRules[0]); guard["outboundTag"] != "blocked" {
		t.Errorf("the private guard must come first and block, got %v", guard)
	}
	if decode(t, got.XrayRules[1])["outboundTag"] != "de" {
		t.Error("sort_index order must survive the compile")
	}
	if !got.NeedsRestart {
		t.Error("a new front changes InboundConfigs, which needs a restart")
	}
}

/*
One rule naming an L7 and an L3 inbound is realised two different ways, so the
operator needs two answers. A per-row diagnostic can only describe one of them.
*/
func TestMultiSubjectRuleFansOutWithCorrectSubjects(t *testing.T) {
	criteria := map[string]json.RawMessage{"domain": json.RawMessage(`["geosite:ads"]`)}
	got := Plan(baseInput(
		[]Subject{internalSubject(1, "vless-in"), deviceSubject(7, "wg-home", "pwg7")},
		[]Rule{{ID: 5, Enable: true, Scope: ScopeSelected, IngressIDs: []int{1, 7},
			Criteria: criteria, Dest: Dest{Kind: DestOutbound, Tag: "warp"}}},
		map[int]int{7: 1},
	))

	var seen []string
	for _, raw := range got.XrayRules {
		rule := decode(t, raw)
		if rule["ip"] != nil {
			continue // the front's private guard
		}
		tags := inboundTags(t, raw)
		seen = append(seen, tags[0])
		if rule["outboundTag"] != "warp" {
			t.Errorf("%v: outboundTag = %v, want warp", tags, rule["outboundTag"])
		}
		domain, _ := json.Marshal(rule["domain"])
		if string(domain) != `["geosite:ads"]` {
			t.Errorf("%v: criteria must be identical on every subject, got %s", tags, domain)
		}
	}
	if len(seen) != 2 || seen[0] != "vless-in" || seen[1] != "peg1" {
		t.Fatalf("subjects = %v, want [vless-in peg1]", seen)
	}
	if len(got.Diags) != 2 {
		t.Fatalf("want one diag per (rule, subject) pair, got %d", len(got.Diags))
	}
	if got.Diags[0].SubjectTag == got.Diags[1].SubjectTag {
		t.Error("the two diags must name different subjects")
	}
	if got.Diags[0].Pattern != PatternProxy || got.Diags[1].Pattern != PatternInspected {
		t.Errorf("patterns = %q,%q; want proxy then inspected", got.Diags[0].Pattern, got.Diags[1].Pattern)
	}
}

/*
`user` can never match traffic from a kernel ingress: Xray's tun handler builds a
MemoryUser with no Email, so the matcher returns false for every packet. The rule
has to be refused rather than accepted and left to never match.
*/
func TestCriteriaMaskIsTheIntersection(t *testing.T) {
	l7 := internalSubject(1, "vless-in")
	l7.CriteriaMask = []string{"domain", "user"}
	l3 := deviceSubject(7, "wg-home", "pwg7")
	l3.CriteriaMask = []string{"domain"}

	got := Plan(baseInput(
		[]Subject{l7, l3},
		[]Rule{{ID: 5, Enable: true, Scope: ScopeSelected, IngressIDs: []int{1, 7},
			Criteria: map[string]json.RawMessage{"user": json.RawMessage(`["a@b"]`)},
			Dest:     Dest{Kind: DestOutbound, Tag: "warp"}}},
		map[int]int{7: 1},
	))

	refusals := got.Refusals()
	if len(refusals) != 1 {
		t.Fatalf("want exactly the L3 subject refused, got %d refusals", len(refusals))
	}
	if refusals[0].SubjectTag != "wg-home" || refusals[0].Args["criterion"] != "user" {
		t.Fatalf("refusal = %+v, want it to name user on wg-home", refusals[0])
	}
	// The L7 subject still routes: one rule may be honoured on one side and
	// refused on the other, and saying so is the point of a per-pair diag.
	if len(got.XrayRules) != 2 {
		t.Fatalf("want the guard plus the honoured L7 rule, got %d", len(got.XrayRules))
	}
}

/*
The cell-B leak guard. egress.RuleSpec has no Mark field, so a marked socket with
no ip rule to catch it routes via main and leaves with the SERVER's address --
direct rather than dark, which inverts what internal/egress guarantees.
*/
func TestPlanNeverEmitsAMarkWithoutItsRule(t *testing.T) {
	got := Plan(Input{
		Subjects: []Subject{internalSubject(1, "vless-in"), deviceSubject(7, "wg-home", "pwg7")},
		Rules: []Rule{
			{ID: 1, Enable: true, Scope: ScopeAll, Dest: Dest{Kind: DestExit, ExitID: 3}},
		},
		Exits: []ResolvedExit{{
			ID: 3, Label: "office", Kind: core.ExitDevice,
			Handle: core.ExitHandle{Device: "ppp0", Source: core.SourceOwnerDaemon},
		}},
		Blackhole: "blocked", Direct: "direct", FrontIDFor: frontIDs(map[int]int{7: 1}),
	})

	for _, raw := range append(append([]json.RawMessage{}, got.XrayRules...), got.Outbounds...) {
		if strings.Contains(string(raw), "mark") {
			t.Fatalf("emitted a mark with no ip rule to catch it: %s", raw)
		}
	}
	if len(got.Refusals()) != 2 {
		t.Fatalf("a device exit has no driver yet and must be refused on both subjects, got %d", len(got.Refusals()))
	}
}

func TestPanelSourceOwnerExitIsRefused(t *testing.T) {
	got := Plan(Input{
		Subjects: []Subject{internalSubject(1, "vless-in")},
		Rules:    []Rule{{ID: 1, Enable: true, Scope: ScopeSelected, IngressIDs: []int{1}, Dest: Dest{Kind: DestExit, ExitID: 3}}},
		Exits: []ResolvedExit{{
			ID: 3, Label: "office", Kind: core.ExitDevice,
			Handle: core.ExitHandle{Device: "ppp0", Source: core.SourceOwnerPanel},
		}},
		Blackhole: "blocked", Direct: "direct", FrontIDFor: frontIDs(nil),
	})

	refusals := got.Refusals()
	if len(refusals) != 1 || refusals[0].MessageKey != KeyNoSnat {
		t.Fatalf("refusals = %+v, want one naming the missing source NAT", refusals)
	}
	if refusals[0].Args["exit"] != "office" {
		t.Errorf("the refusal must name the exit, got %v", refusals[0].Args)
	}
}

// A socks-port exit is the one exit shape that works today, and it is
// injectMtprotoEgress generalised: one synthesized outbound, dialled on loopback.
func TestSocksExitSynthesizesItsOutbound(t *testing.T) {
	got := Plan(Input{
		Subjects: []Subject{internalSubject(1, "vless-in")},
		Rules:    []Rule{{ID: 1, Enable: true, Scope: ScopeSelected, IngressIDs: []int{1}, Dest: Dest{Kind: DestExit, ExitID: 4}}},
		Exits: []ResolvedExit{{
			ID: 4, Label: "bridge", Kind: core.ExitSocksPort,
			Handle: core.ExitHandle{SocksPort: 2398, Source: core.SourceOwnerDaemon},
		}},
		Blackhole: "blocked", Direct: "direct", FrontIDFor: frontIDs(nil),
	})

	if len(got.Refusals()) != 0 {
		t.Fatalf("a socks exit is realisable today, got %+v", got.Refusals())
	}
	if len(got.Outbounds) != 1 {
		t.Fatalf("want one synthesized outbound, got %d", len(got.Outbounds))
	}
	outbound := decode(t, got.Outbounds[0])
	if outbound["tag"] != "pex4" || outbound["protocol"] != "socks" {
		t.Fatalf("outbound = %v, want a socks outbound tagged pex4", outbound)
	}
	if decode(t, got.XrayRules[0])["outboundTag"] != "pex4" {
		t.Error("the rule must target the outbound the compile just synthesized")
	}
}

/*
An "all" rule expands at its own position rather than being emitted untagged.
That keeps first-match ordering exact AND keeps L3 ingresses covered, since a
bare untagged rule would never carry the peg tag their traffic actually arrives on.
*/
func TestExpandAllScopePreservesPosition(t *testing.T) {
	got := Plan(baseInput(
		[]Subject{internalSubject(1, "a-in"), internalSubject(2, "b-in")},
		[]Rule{
			{ID: 1, SortIndex: 0, Enable: true, Scope: ScopeSelected, IngressIDs: []int{1}, Dest: Dest{Kind: DestOutbound, Tag: "first"}},
			{ID: 2, SortIndex: 1, Enable: true, Scope: ScopeAll, Dest: Dest{Kind: DestBlock}},
			{ID: 3, SortIndex: 2, Enable: true, Scope: ScopeSelected, IngressIDs: []int{1}, Dest: Dest{Kind: DestOutbound, Tag: "last"}},
		},
		nil,
	))

	var order []string
	for _, raw := range got.XrayRules {
		rule := decode(t, raw)
		tag, _ := rule["outboundTag"].(string)
		order = append(order, inboundTags(t, raw)[0]+":"+tag)
	}
	want := []string{"a-in:first", "a-in:blocked", "a-in:last", "b-in:blocked"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

// A disabled rule is not a hidden rule: it must contribute nothing at all.
func TestDisabledRuleEmitsNothing(t *testing.T) {
	got := Plan(baseInput(
		[]Subject{deviceSubject(7, "wg-home", "pwg7")},
		[]Rule{{ID: 1, Enable: false, Scope: ScopeSelected, IngressIDs: []int{7}, Dest: Dest{Kind: DestBlock}}},
		map[int]int{7: 1},
	))

	if len(got.XrayRules) != 0 || len(got.Kernel) != 0 || len(got.Fronts) != 0 {
		t.Fatalf("a disabled rule must not front its ingress: %d rules, %d kernel, %d fronts",
			len(got.XrayRules), len(got.Kernel), len(got.Fronts))
	}
}

// A blocked ingress is reported with the core's own reason rather than fronted.
func TestBlockedIngressIsReportedNotFronted(t *testing.T) {
	subject := Subject{InboundID: 3, Tag: "mt-1",
		Handle: core.IngressHandle{BlockedKey: "pages.xray.subjects.reasonBridgeOff"}}
	got := Plan(baseInput(
		[]Subject{subject},
		[]Rule{{ID: 1, Enable: true, Scope: ScopeSelected, IngressIDs: []int{3}, Dest: Dest{Kind: DestBlock}}},
		map[int]int{3: 1},
	))

	if len(got.Kernel) != 0 || len(got.XrayRules) != 0 {
		t.Fatal("a blocked ingress must not be fronted or emitted")
	}
	refusals := got.Refusals()
	if len(refusals) != 1 || refusals[0].MessageKey != "pages.xray.subjects.reasonBridgeOff" {
		t.Fatalf("refusals = %+v, want the core's own blocked key", refusals)
	}
}
