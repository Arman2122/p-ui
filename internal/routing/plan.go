/*
Package routing compiles operator intent into the two artifacts that realise it:
the Xray rules array, and the kernel state internal/egress converges.

Pure by construction — no database, no netlink, no xray-core types — because the
whole value of the compile is that a preview and a save derive the same answer
from the same input. Everything protocol-specific arrives as a core.IngressHandle
or a core.ExitHandle, so a rule may name any core on either side and this file
never learns which one.
*/
package routing

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/egress"
)

// Destination kinds a rule may carry. Closed vocabulary: an unknown kind is a
// refusal rather than a fall-through to direct.
const (
	DestOutbound = "outbound"
	DestBalancer = "balancer"
	DestExit     = "exit"
	DestDirect   = "direct"
	DestBlock    = "block"
)

// Scopes. "all" expands at compile time to one rule per routable subject, which
// is what keeps ordering exact while still covering L3 ingresses.
const (
	ScopeSelected = "selected"
	ScopeAll      = "all"
)

// Subject is one inbound as a rule may name it, with its core's answer already
// resolved. CriteriaMask names the criteria that can actually work on it.
type Subject struct {
	InboundID    int
	Tag          string
	Handle       core.IngressHandle
	Inspect      bool
	CriteriaMask []string
}

// Dest is where a rule sends what it matches.
type Dest struct {
	Kind   string
	Tag    string
	ExitID int
}

// Rule is one row of operator intent.
type Rule struct {
	ID         int
	SortIndex  int
	Enable     bool
	Scope      string
	IngressIDs []int
	Dest       Dest
	Criteria   map[string]json.RawMessage
}

// ResolvedExit is one uplink with its core's handle already resolved.
type ResolvedExit struct {
	ID     int
	Label  string
	Kind   core.ExitHandleKind
	Handle core.ExitHandle
}

// FrontSpec is a front the compile wants Xray to create for an L3 ingress.
type FrontSpec struct {
	ID    int
	Sniff bool
}

// Input is everything the compile needs. Blackhole and Direct are found by
// PROTOCOL by the caller, never by the tag the default template happens to use.
type Input struct {
	Rules     []Rule
	Subjects  []Subject
	Exits     []ResolvedExit
	Blackhole string
	Direct    string
	// FrontIDFor gives the panel-owned egresses row id for an ingress inbound.
	// A function rather than a map so the caller owns allocation and this stays pure.
	FrontIDFor func(inboundID int) (int, bool)
}

// Compiled is the whole answer: what Xray runs, what the kernel converges, and
// what the operator is told about each (rule, subject) pair.
type Compiled struct {
	XrayRules    []json.RawMessage
	Outbounds    []json.RawMessage
	Fronts       []FrontSpec
	Kernel       []egress.Egress
	Diags        []Diag
	NeedsRestart bool
}

// Plan derives every artifact from intent. It never returns an error: a rule it
// cannot realise becomes a Diag the operator reads, so a preview can show the
// whole picture instead of stopping at the first problem. Refusals reports the
// subset that must block a save.
func Plan(in Input) Compiled {
	out := Compiled{}
	bySubject := groupRules(in)

	subjects := append([]Subject(nil), in.Subjects...)
	sort.Slice(subjects, func(i, j int) bool { return subjects[i].InboundID < subjects[j].InboundID })

	for _, subject := range subjects {
		rules := bySubject[subject.InboundID]
		if len(rules) == 0 {
			continue
		}
		if subject.Handle.BlockedKey != "" {
			for _, rule := range rules {
				out.Diags = append(out.Diags, Diag{
					RuleID: rule.ID, SubjectTag: subject.Tag, Severity: SeverityBlocked,
					Pattern: PatternInert, MessageKey: subject.Handle.BlockedKey,
				})
			}
			continue
		}

		tag, pattern := subject.Handle.Tag, PatternProxy
		if tag == "" {
			// An L3 ingress is always fronted in this phase: the kernel FIB has no
			// domain or port vocabulary, so criteria can only be matched inside Xray.
			frontID, ok := in.FrontIDFor(subject.InboundID)
			if !ok || !egress.ValidID(frontID) {
				for _, rule := range rules {
					out.Diags = append(out.Diags, Diag{
						RuleID: rule.ID, SubjectTag: subject.Tag, Severity: SeverityInert,
						Pattern: PatternInert, MessageKey: KeyNoFront,
					})
				}
				continue
			}
			tag = egress.Device(frontID)
			pattern = PatternInspected
			out.Kernel = append(out.Kernel, egress.Egress{
				ID: frontID, Type: frontType, Enable: true, Ingress: []string{subject.Handle.Device},
			})
			out.Fronts = append(out.Fronts, FrontSpec{ID: frontID, Sniff: subject.Inspect})
			out.NeedsRestart = true
			// Strictly ahead of this front's own rules: a front is otherwise the one
			// class of forwarded traffic exempt from the template's private block.
			if in.Blackhole != "" {
				out.XrayRules = append(out.XrayRules, guardRule(tag, in.Blackhole))
			}
		}

		for _, rule := range rules {
			emitted, diag, outbound := compileRule(in, rule, subject, tag, pattern)
			out.Diags = append(out.Diags, diag)
			if emitted != nil {
				out.XrayRules = append(out.XrayRules, emitted)
			}
			if outbound != nil {
				out.Outbounds = append(out.Outbounds, outbound)
				out.NeedsRestart = true
			}
		}
	}
	return out
}

// frontType is the driver that terminates an L3 ingress inside Xray. Named here
// rather than imported so this package keeps no dependency on a driver.
const frontType = "xray-tun"

// groupRules expands scope and indexes by subject, preserving operator order.
// An "all" rule is expanded to every routable subject at its own position, which
// is what keeps first-match ordering exact across mechanisms.
func groupRules(in Input) map[int][]Rule {
	out := map[int][]Rule{}
	rules := append([]Rule(nil), in.Rules...)
	sort.SliceStable(rules, func(i, j int) bool { return rules[i].SortIndex < rules[j].SortIndex })
	for _, rule := range rules {
		if !rule.Enable {
			continue
		}
		ids := rule.IngressIDs
		if rule.Scope == ScopeAll {
			ids = nil
			for _, subject := range in.Subjects {
				ids = append(ids, subject.InboundID)
			}
		}
		for _, id := range ids {
			out[id] = append(out[id], rule)
		}
	}
	return out
}

/*
compileRule emits one Xray rule for one (rule, subject) pair.

target() is where the N-by-N matrix collapses to four answers, and the cell that
is deliberately missing is the marked one: THE COMPILE NEVER EMITS A MARKED
OUTBOUND WITHOUT ITS MATCHING ip rule fwmark. egress.RuleSpec has no Mark field,
so a marked socket with no rule to catch it routes via main and leaves with the
server's own address — direct rather than dark, which inverts the one property
internal/egress guarantees.
*/
func compileRule(in Input, rule Rule, subject Subject, tag, pattern string) (json.RawMessage, Diag, json.RawMessage) {
	diag := Diag{RuleID: rule.ID, SubjectTag: subject.Tag, Pattern: pattern, Severity: SeverityOK}

	if bad, ok := forbiddenCriterion(rule, subject); ok {
		diag.Severity = SeverityInert
		diag.Pattern = PatternInert
		diag.MessageKey = KeyCriterionUnsupported
		diag.Args = map[string]string{"criterion": bad, "subject": subject.Tag}
		return nil, diag, nil
	}

	body := map[string]json.RawMessage{}
	for key, value := range rule.Criteria {
		body[key] = value
	}
	body["type"] = json.RawMessage(`"field"`)
	body["inboundTag"] = mustJSON([]string{tag})

	var outbound json.RawMessage
	switch rule.Dest.Kind {
	case DestOutbound:
		body["outboundTag"] = mustJSON(rule.Dest.Tag)
	case DestBalancer:
		body["balancerTag"] = mustJSON(rule.Dest.Tag)
	case DestDirect:
		body["outboundTag"] = mustJSON(in.Direct)
	case DestBlock:
		body["outboundTag"] = mustJSON(in.Blackhole)
	case DestExit:
		exit, found := findExit(in.Exits, rule.Dest.ExitID)
		if !found {
			diag.Severity, diag.Pattern, diag.MessageKey = SeverityInert, PatternInert, KeyExitMissing
			return nil, diag, nil
		}
		switch exit.Kind {
		case core.ExitXrayOutbound:
			body["outboundTag"] = mustJSON(exit.Handle.XrayTag)
			diag.Pattern = PatternProxy
		case core.ExitSocksPort:
			name := exitOutboundTag(exit.ID)
			outbound = socksOutbound(name, exit.Handle.SocksPort)
			body["outboundTag"] = mustJSON(name)
			diag.Pattern = PatternProxy
		case core.ExitDevice:
			// Refused until a fwmark rule exists to catch the marked socket, and
			// refused for good when the panel would have to NAT and cannot.
			diag.Severity, diag.Pattern = SeverityInert, PatternInert
			diag.MessageKey = KeyNoExitDriver
			if exit.Handle.Source == core.SourceOwnerPanel {
				diag.MessageKey = KeyNoSnat
			}
			diag.Args = map[string]string{"exit": exit.Label}
			return nil, diag, nil
		default:
			diag.Severity, diag.Pattern, diag.MessageKey = SeverityInert, PatternInert, KeyExitMissing
			return nil, diag, nil
		}
	default:
		diag.Severity, diag.Pattern, diag.MessageKey = SeverityInert, PatternInert, KeyExitMissing
		return nil, diag, nil
	}
	return mustJSON(body), diag, outbound
}

// forbiddenCriterion reports the first criterion this subject cannot honour. The
// mask is the INTERSECTION when a rule names several subjects, because the
// editor enforces one set of criteria per rule rather than per subject.
func forbiddenCriterion(rule Rule, subject Subject) (string, bool) {
	if subject.CriteriaMask == nil {
		return "", false
	}
	keys := make([]string, 0, len(rule.Criteria))
	for key := range rule.Criteria {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		allowed := false
		for _, ok := range subject.CriteriaMask {
			if ok == key {
				allowed = true
				break
			}
		}
		if !allowed {
			return key, true
		}
	}
	return "", false
}

func findExit(exits []ResolvedExit, id int) (ResolvedExit, bool) {
	for _, exit := range exits {
		if exit.ID == id {
			return exit, true
		}
	}
	return ResolvedExit{}, false
}

// exitOutboundTag names a synthesized outbound. The prefix round-trips through
// this one function so nothing else has to know the shape.
func exitOutboundTag(id int) string { return fmt.Sprintf("pex%d", id) }

func socksOutbound(tag string, port int) json.RawMessage {
	return mustJSON(map[string]any{
		"tag":      tag,
		"protocol": "socks",
		"settings": map[string]any{
			"servers": []any{map[string]any{"address": "127.0.0.1", "port": port}},
		},
	})
}

func guardRule(tag, blackhole string) json.RawMessage {
	return mustJSON(map[string]any{
		"type": "field", "inboundTag": []string{tag},
		"ip": []string{"geoip:private"}, "outboundTag": blackhole,
	})
}

// mustJSON marshals values this package built itself. A failure would mean a
// map with an unmarshalable value, which no caller can construct through Input.
func mustJSON(v any) json.RawMessage {
	raw, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`null`)
	}
	return raw
}
