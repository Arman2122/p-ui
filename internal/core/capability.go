package core

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
)

/*
One implementation of "may this inbound do X".

These rules were implemented three times in three shapes — raw JSON strings in
internal/web/service, pre-parsed maps in internal/sub, and form objects in
frontend/src/lib/xray/protocol-capabilities.ts — with comments in each pointing
at the others and no test crossing the boundary. Two of them (tls, reality)
existed only in TypeScript, so the REST API and the Telegram bot could create
configurations the UI refuses.

The rules are data, not code, so the same table can be evaluated in Go and in
TypeScript. The grammar is deliberately tiny: a rule is a disjunction of
conjunctions (DNF) over three operators, one level deep, addressing two roots.
Anything that needs more than this is code, not a rule — see the note on Grant
in ResolveAll.
*/

// Op is a clause operator. The set is closed on purpose: every rule this panel
// has ever needed fits these three, and a fourth would mean the TypeScript twin
// has to grow too.
type Op string

const (
	// OpIn matches when the field equals one of Values.
	OpIn Op = "in"
	// OpSet matches a field holding a real value, i.e. not "" and not "none".
	OpSet Op = "set"
	// OpPrefix matches when the field starts with one of Values.
	OpPrefix Op = "prefix"
)

// Clause is one condition. Not inverts it, which keeps rules like "every
// protocol except mtproto" from having to enumerate every other protocol.
type Clause struct {
	Field  string   `json:"field"`
	Op     Op       `json:"op"`
	Values []string `json:"values,omitempty"`
	Not    bool     `json:"not,omitempty"`
}

// Term is a conjunction; every clause must hold.
type Term struct {
	All []Clause `json:"all"`
}

// Rule is a disjunction of terms; any term satisfies it.
type Rule struct {
	Any []Term `json:"any"`
}

// Facts is the inbound reduced to the fields rules may address. Field names are
// "protocol", "stream.<key>" and "settings.<key>"; nothing else is addressable,
// so a rule can never reach into arbitrary config.
type Facts struct {
	Protocol string            `json:"protocol"`
	Stream   map[string]string `json:"stream,omitempty"`
	Settings map[string]string `json:"settings,omitempty"`
}

func (f Facts) lookup(field string) string {
	if field == "protocol" {
		return f.Protocol
	}
	if key, ok := strings.CutPrefix(field, "stream."); ok {
		return f.Stream[key]
	}
	if key, ok := strings.CutPrefix(field, "settings."); ok {
		return f.Settings[key]
	}
	return ""
}

func (c Clause) eval(f Facts) bool {
	value := f.lookup(c.Field)
	var got bool
	switch c.Op {
	case OpIn:
		for _, want := range c.Values {
			if value == want {
				got = true
				break
			}
		}
	case OpSet:
		// "none" is xray's explicit off-switch and reads as unset everywhere.
		got = value != "" && value != "none"
	case OpPrefix:
		for _, want := range c.Values {
			if want != "" && strings.HasPrefix(value, want) {
				got = true
				break
			}
		}
	default:
		return false
	}
	if c.Not {
		return !got
	}
	return got
}

// Eval reports whether the rule holds. A rule with no terms is false: an
// unknown capability must never read as permitted.
func (r Rule) Eval(f Facts) bool {
	for _, term := range r.Any {
		if len(term.All) == 0 {
			continue
		}
		satisfied := true
		for _, clause := range term.All {
			if !clause.eval(f) {
				satisfied = false
				break
			}
		}
		if satisfied {
			return true
		}
	}
	return false
}

// Capability names. These cross the API and the language boundary, so they are
// constants rather than loose strings.
const (
	CapTLS       = "tls"
	CapReality   = "reality"
	CapTLSFlow   = "tlsFlow"
	CapStream    = "stream"
	CapSniffing  = "sniffing"
	CapFallbacks = "fallbacks"
	CapSS2022    = "ss2022"
)

func in(field string, values ...string) Clause {
	return Clause{Field: field, Op: OpIn, Values: values}
}

// capabilityRules is the single source of truth, emitted to the frontend by
// tools/openapigen and cross-checked by a golden fixture.
var capabilityRules = map[string]Rule{
	// Hysteria carries its own TLS; the rest need an eligible transport.
	CapTLS: {Any: []Term{
		{All: []Clause{in("protocol", "hysteria")}},
		{All: []Clause{
			in("protocol", "vmess", "vless", "trojan", "shadowsocks"),
			in("stream.network", "tcp", "ws", "http", "grpc", "httpupgrade", "xhttp"),
		}},
	}},
	CapReality: {Any: []Term{
		{All: []Clause{
			in("protocol", "vless", "trojan"),
			in("stream.network", "tcp", "http", "grpc", "xhttp"),
		}},
	}},
	// XTLS Vision: classic raw TCP over TLS/REALITY, or XHTTP where VLESS-level
	// encryption (vlessenc / ML-KEM) stands in for the transport TLS.
	CapTLSFlow: {Any: []Term{
		{All: []Clause{
			in("protocol", "vless"),
			in("stream.network", "tcp"),
			in("stream.security", "tls", "reality"),
		}},
		{All: []Clause{
			in("protocol", "vless"),
			in("stream.network", "xhttp"),
			{Field: "settings.encryption", Op: OpSet},
		}},
		{All: []Clause{
			in("protocol", "vless"),
			in("stream.network", "xhttp"),
			{Field: "settings.decryption", Op: OpSet},
		}},
	}},
	CapStream: {Any: []Term{
		{All: []Clause{in("protocol", "vmess", "vless", "trojan", "shadowsocks", "hysteria", "wireguard", "tunnel")}},
	}},
	// mtproto is served by the mtg sidecar, not Xray, so Xray's sniffing block
	// does not apply. Negated so a new core does not have to be listed here.
	CapSniffing: {Any: []Term{
		{All: []Clause{{Field: "protocol", Op: OpIn, Values: []string{"mtproto"}, Not: true}}},
	}},
	// Fallbacks are raw-TCP only — stricter than tlsFlow, which also allows XHTTP.
	CapFallbacks: {Any: []Term{
		{All: []Clause{
			in("protocol", "vless", "trojan"),
			in("stream.network", "tcp"),
			in("stream.security", "tls", "reality"),
		}},
	}},
	CapSS2022: {Any: []Term{
		{All: []Clause{
			in("protocol", "shadowsocks"),
			{Field: "settings.method", Op: OpPrefix, Values: []string{"2022"}},
		}},
	}},
}

// CapabilityNames returns every rule name, sorted, so callers and codegen see a
// stable order.
func CapabilityNames() []string {
	out := make([]string, 0, len(capabilityRules))
	for name := range capabilityRules {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

// CapabilityRules returns a copy of the rule table for serialisation.
func CapabilityRules() map[string]Rule {
	out := make(map[string]Rule, len(capabilityRules))
	for name, rule := range capabilityRules {
		out[name] = rule
	}
	return out
}

// Can reports whether the named capability is permitted for these facts. An
// unknown name is false, never true.
func Can(name string, f Facts) bool {
	rule, ok := capabilityRules[name]
	if !ok {
		return false
	}
	return rule.Eval(f)
}

// ResolveAll answers every capability at once, which is what the API hands the
// UI so the frontend never re-derives a rule.
func ResolveAll(f Facts) map[string]bool {
	out := make(map[string]bool, len(capabilityRules))
	for name, rule := range capabilityRules {
		out[name] = rule.Eval(f)
	}
	return out
}

// FactsFromJSON builds Facts from an inbound's stored columns. Only scalars are
// addressable: nested objects and arrays are dropped rather than flattened, so a
// rule cannot come to depend on config shape it should not see.
func FactsFromJSON(protocol, settingsJSON, streamJSON string) Facts {
	return Facts{
		Protocol: protocol,
		Settings: scalarFields(settingsJSON),
		Stream:   scalarFields(streamJSON),
	}
}

func scalarFields(raw string) map[string]string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil
	}
	return Scalars(parsed)
}

// Scalars flattens an already-parsed config object for use as Facts. Callers
// that hold decoded JSON — internal/sub works this way — use it instead of
// re-encoding just to call FactsFromJSON.
func Scalars(parsed map[string]any) map[string]string {
	out := make(map[string]string, len(parsed))
	for key, value := range parsed {
		switch v := value.(type) {
		case string:
			out[key] = v
		case bool:
			out[key] = strconv.FormatBool(v)
		case float64:
			out[key] = strconv.FormatFloat(v, 'f', -1, 64)
		}
	}
	return out
}
