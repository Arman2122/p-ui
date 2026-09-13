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
	"strings"

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
	InboundID int
	Tag       string
	Handle    core.IngressHandle
	/*
		Bridged is an internal subject whose traffic reaches Xray through a
		loopback inbound the PANEL dials into, rather than one a remote client
		connects to. It changes nothing about matching and one thing about
		containment: the dialer on the other side is a core on this host, so a
		rule pointing at a private address would send it back into its own
		bridge.
	*/
	Bridged      bool
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
	// Inspect forces the front to sniff even when no criterion needs it. A
	// domain criterion implies it; this is the manual override.
	Inspect bool
}

/*
NeedsInspection reports whether this rule can only match once the front reads
the payload.

A domain criterion IS the intent: an L3 ingress carries no name anywhere in the
packet, so a rule matching one is asking for the TLS SNI, HTTP Host or QUIC
name to be read. Deriving it rather than making the operator find a second
switch is what stops a domain rule saving and never matching -- the whole class
of bug F060's criteria mask exists to remove.
*/
func (r Rule) NeedsInspection() bool {
	if r.Inspect {
		return true
	}
	for key := range r.Criteria {
		if core.Criterion(key).NeedsInspection() {
			return true
		}
	}
	return false
}

// ResolvedExit is one uplink with its core's handle already resolved.
type ResolvedExit struct {
	ID     int
	Label  string
	Kind   core.ExitHandleKind
	Handle core.ExitHandle
}

/*
FrontSpec is a front the compile wants Xray to create for an L3 ingress.

Sniff makes the front read the payload for a domain. It is asked for by the
rules on that subject rather than configured per inbound: a front that sniffs
when nothing needs it pays the cost of a cached reader on every connection for
an answer no rule reads.
*/
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
	XrayRules []json.RawMessage
	Outbounds []json.RawMessage
	Fronts    []FrontSpec
	/*
		InspectTags are the tags of INTERNAL ingresses whose rules need the
		payload read: an Xray inbound the operator owns, or the loopback bridge
		the panel builds for a sidecar core.

		Separate from Fronts because the panel does not create these inbounds, it
		amends them -- and it amends only the generated config, never the stored
		row, so the operator's own sniffing setting is untouched.
	*/
	InspectTags []string
	Diags       []Diag
}

// Plan derives every artifact from intent. It never returns an error: a rule it
// cannot realise becomes a Diag the operator reads, so a preview can show the
// whole picture instead of stopping at the first problem. Refusals reports the
// subset that must block a save.
func Plan(in Input) Compiled {
	out := Compiled{}
	bySubject := groupRules(in)
	// An exit's outbound is named after the exit, not after the pair that asked
	// for it, so every extra (rule, subject) pointing at one exit would repeat
	// it verbatim — and xray refuses a duplicate tag by refusing the whole load.
	emitted := map[string]bool{}

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
		if tag != "" && inspectedBy(rules, subject) {
			/*
				An internal ingress is an Xray inbound already, and Xray only
				matches a domain when that inbound sniffs. The panel's own default
				for a new inbound is sniffing OFF, so a domain rule on a VLESS
				inbound saved, displayed as active, and never matched -- F060 on
				the kinds people actually use.

				The rule is the intent, so the generated config carries the block.
				The stored inbound is not touched: an operator who turned sniffing
				off did that for their traffic, not for the router.
			*/
			out.InspectTags = append(out.InspectTags, tag)
		}
		if tag == "" {
			// An L3 ingress is always fronted in this phase: the kernel FIB has no
			// domain or port vocabulary, so criteria can only be matched inside Xray.
			frontID, ok := in.FrontIDFor(subject.InboundID)
			if !ok || !egress.ValidID(frontID) {
				for _, rule := range rules {
					out.Diags = append(out.Diags, Diag{
						RuleID: rule.ID, SubjectTag: subject.Tag, Severity: SeverityInert,
						Pattern: PatternInert, MessageKey: KeyNoFront,
						Args: map[string]string{"subject": subject.Tag},
					})
				}
				continue
			}
			tag = egress.Device(frontID)
			pattern = PatternInspected
			out.Fronts = append(out.Fronts, FrontSpec{ID: frontID, Sniff: inspectedBy(rules, subject)})
			// Strictly ahead of this front's own rules: a front is otherwise the one
			// class of forwarded traffic exempt from the template's private block.
			out.XrayRules = append(out.XrayRules, guardRule(tag, blackholeTag(in, &out, emitted)))
		} else if subject.Bridged {
			/*
				A bridged subject is dialled into by a core on this host, so the
				same containment applies for the same reason a front needs it: an
				operator rule is spliced AHEAD of the template's own private
				block, so without this a client naming the bridge's own address
				drives core -> bridge -> router -> bridge in a circle.

				It catches an address, not a name: the guard matches IP literals
				and the shipped template resolves nothing, so a forward to a
				hostname that happens to resolve to loopback is not covered.
			*/
			out.XrayRules = append(out.XrayRules, guardRule(tag, blackholeTag(in, &out, emitted)))
		}

		for _, rule := range rules {
			xrayRule, diag, outbound := compileRule(in, rule, subject, tag, pattern)
			out.Diags = append(out.Diags, diag)
			if xrayRule != nil {
				out.XrayRules = append(out.XrayRules, xrayRule)
			}
			if outbound == nil {
				continue
			}
			if outTag := synthesizedTag(outbound); !emitted[outTag] {
				emitted[outTag] = true
				out.Outbounds = append(out.Outbounds, outbound)
			}
		}
	}
	relaxScopeAll(in, &out)
	return out
}

// relaxScopeAll: "every routable inbound" — once an all-rule compiled for one
// subject, a subject that cannot carry it is a limitation shown, not a refusal.
func relaxScopeAll(in Input, out *Compiled) {
	all := map[int]bool{}
	for _, rule := range in.Rules {
		if rule.Scope == ScopeAll {
			all[rule.ID] = true
		}
	}
	compiled := map[int]bool{}
	for _, diag := range out.Diags {
		if all[diag.RuleID] && (diag.Severity == SeverityOK || diag.Severity == SeverityWarn) {
			compiled[diag.RuleID] = true
		}
	}
	for i, diag := range out.Diags {
		if compiled[diag.RuleID] && (diag.Severity == SeverityInert || diag.Severity == SeverityBlocked) {
			out.Diags[i].Severity = SeverityWarn
		}
	}
}

// blackholeTag names the outbound a containment guard sends traffic to. The
// guard is the whole reason a fronted or bridged subject is not exempt from the
// template's private block, so one is synthesized rather than skipped when the
// operator's own template has none.
func blackholeTag(in Input, out *Compiled, emitted map[string]bool) string {
	if in.Blackhole != "" {
		return in.Blackhole
	}
	if !emitted[synthBlockTag] {
		emitted[synthBlockTag] = true
		out.Outbounds = append(out.Outbounds, blackholeOutbound(synthBlockTag))
	}
	return synthBlockTag
}

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

The N-by-N matrix collapses to five answers here, and the marked one carries the
invariant: A MARKED OUTBOUND IS ONLY EMITTED FOR AN EXIT WHOSE ROW CONVERGES THE
MATCHING ip rule fwmark. A marked socket with no rule to catch it routes via main
and leaves with the server's own address — direct rather than dark, which inverts
the one property internal/egress guarantees. The row is what ties them together:
the same enabled egress that answers ExitDevice here is the one whose Fill sets
Marked, and the service layer converges the kernel before Xray is restarted.
*/
type narrowResult int

const (
	narrowSilent narrowResult = iota
	narrowWarned
	narrowRefused
)

// narrowToTCP pins a rule bound for a TCP-only exit to "tcp": UDP alone is
// refused; both, or nothing said, is narrowed and the operator warned.
func narrowToTCP(body map[string]json.RawMessage) narrowResult {
	raw, ok := body["network"]
	if !ok {
		body["network"] = json.RawMessage(`"tcp"`)
		return narrowWarned
	}
	var network string
	if json.Unmarshal(raw, &network) != nil {
		body["network"] = json.RawMessage(`"tcp"`)
		return narrowWarned
	}
	hasTCP, hasUDP := false, false
	for _, part := range strings.Split(network, ",") {
		switch strings.TrimSpace(strings.ToLower(part)) {
		case "tcp":
			hasTCP = true
		case "udp":
			hasUDP = true
		}
	}
	switch {
	case hasUDP && !hasTCP:
		return narrowRefused
	case hasUDP:
		body["network"] = json.RawMessage(`"tcp"`)
		return narrowWarned
	}
	return narrowSilent
}

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
		if in.Direct == "" {
			diag.Severity, diag.Pattern, diag.MessageKey = SeverityInert, PatternInert, KeyNoDefaultOutbound
			diag.Args = map[string]string{"protocol": "freedom"}
			return nil, diag, nil
		}
		body["outboundTag"] = mustJSON(in.Direct)
	case DestBlock:
		/*
			A template with no blackhole outbound does not mean the operator wants
			this rule to stop blocking. A blackhole has no egress, so supplying one
			cannot leak anything the refusal was protecting.
		*/
		if in.Blackhole == "" {
			outbound = blackholeOutbound(synthBlockTag)
			body["outboundTag"] = mustJSON(synthBlockTag)
			break
		}
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
			if exit.Handle.TCPOnly {
				switch narrowToTCP(body) {
				case narrowRefused:
					diag.Severity, diag.Pattern, diag.MessageKey = SeverityInert, PatternInert, KeyExitTCPOnly
					diag.Args = map[string]string{"exit": exit.Label}
					return nil, diag, nil
				case narrowWarned:
					diag.Severity, diag.MessageKey = SeverityWarn, KeyExitTCPNarrowed
					diag.Args = map[string]string{"exit": exit.Label}
				}
			}
			name := exitOutboundTag(exit.ID)
			outbound = socksOutbound(name, exit.Handle.SocksPort)
			body["outboundTag"] = mustJSON(name)
			diag.Pattern = PatternProxy
		case core.ExitDevice:
			// Still refused for good when the panel would have to NAT and cannot:
			// a route that looks healthy and delivers nothing is the worse answer.
			if exit.Handle.Source == core.SourceOwnerPanel || exit.Handle.Device == "" {
				diag.Severity, diag.Pattern = SeverityInert, PatternInert
				diag.MessageKey = KeyNoSnat
				if exit.Handle.Device == "" {
					diag.MessageKey = KeyNoExitDriver
				}
				diag.Args = map[string]string{"exit": exit.Label}
				return nil, diag, nil
			}
			name := exitOutboundTag(exit.ID)
			outbound = markedOutbound(name, egress.Mark(exit.ID))
			body["outboundTag"] = mustJSON(name)
			diag.Pattern = PatternMarked
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

// synthBlockTag shares the reserved exit prefix but ends in letters, so it can
// never collide with pex<id>. Validating operator tags against it is F202.
const synthBlockTag = "pexblock"

func blackholeOutbound(tag string) json.RawMessage {
	return mustJSON(map[string]any{"tag": tag, "protocol": "blackhole"})
}

// synthesizedTag reads back the tag compileRule stamped, which is what identity
// means for an outbound as far as xray-core's handler registry is concerned.
func synthesizedTag(outbound json.RawMessage) string {
	var tagged struct {
		Tag string `json:"tag"`
	}
	_ = json.Unmarshal(outbound, &tagged)
	return tagged.Tag
}

/*
markedOutbound re-originates the traffic and stamps the socket so the kernel can
steer it.

freedom, not a proxy: the far end of a device exit speaks no proxy protocol. The
mark is the whole mechanism — it is what internal/egress matches to send this
socket into the exit's own table, and re-originating here is what makes the
kernel choose the uplink's address as the source, so nothing has to NAT.
*/
func markedOutbound(tag string, mark uint32) json.RawMessage {
	return mustJSON(map[string]any{
		"tag":      tag,
		"protocol": "freedom",
		// The template direct's finalRules: freedom resolves a name before an ip
		// rule, so "localhost" from a bridge is blocked here rather than dialled.
		"settings": map[string]any{
			"domainStrategy": "AsIs",
			"finalRules": []map[string]any{
				{"action": "block", "ip": []string{"geoip:private"}},
				{"action": "allow"},
			},
		},
		"streamSettings": map[string]any{
			"sockopt": map[string]any{"mark": mark},
		},
	})
}

func socksOutbound(tag string, port int) json.RawMessage {
	return mustJSON(map[string]any{
		"tag":      tag,
		"protocol": "socks",
		"settings": map[string]any{
			"servers": []any{map[string]any{"address": "127.0.0.1", "port": port}},
		},
	})
}

/*
inspectedBy reports whether any rule that will actually COMPILE for this subject
needs the payload read.

The forbiddenCriterion check is the load-bearing half. A rule can name a subject
whose core cannot match its criteria -- a domain rule on an mtproto ingress, or
on a future per-session core that routes by address alone -- and that rule is
refused as inert further down. Asking for the payload anyway would put a sniffer
on every connection through that inbound to answer a question no rule will ever
read, which is a cost paid forever for nothing.

The inert rule is still reported to the operator; this only decides whether the
core is made to work for it.
*/
func inspectedBy(rules []Rule, subject Subject) bool {
	for _, rule := range rules {
		if !rule.Enable || !rule.NeedsInspection() {
			continue
		}
		if _, forbidden := forbiddenCriterion(rule, subject); forbidden {
			continue
		}
		return true
	}
	return false
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
