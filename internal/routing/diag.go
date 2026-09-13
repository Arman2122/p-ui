package routing

// Diag is what the operator is told about one (rule, subject) pair.
//
// Per PAIR and never per row: one rule naming an Xray inbound and a WireGuard
// inbound is realised by two different mechanisms, and one chip cannot describe
// both without lying about one of them.
type Diag struct {
	RuleID     int               `json:"ruleId"`
	SubjectTag string            `json:"subjectTag"`
	Pattern    string            `json:"pattern"`
	Severity   string            `json:"severity"`
	MessageKey string            `json:"messageKey,omitempty"`
	Args       map[string]string `json:"args,omitempty"`
}

// How a pair is realised, as the mechanism chip names it.
const (
	PatternProxy     = "proxy"
	PatternInspected = "inspected"
	PatternKernel    = "kernel"
	PatternMarked    = "marked"
	PatternInert     = "inert"
)

// Severity. Only inert and blocked stop traffic; warn is advisory.
const (
	SeverityOK      = "ok"
	SeverityWarn    = "warn"
	SeverityInert   = "inert"
	SeverityBlocked = "blocked"
)

// Message keys. Translation keys rather than sentences: the panel ships two
// locales and these reach the operator.
// A key here must have an emit site. selfLoop sat in this block from e30b1841
// with none, and nothing could see it: the dead-keys scan starts from the
// locale file, and no string was ever shipped for it either. Ledger L6.
const (
	KeyNoFront              = "pages.xray.routing.refuse.noFront"
	KeyCriterionUnsupported = "pages.xray.routing.refuse.criterionUnsupported"
	KeyExitMissing          = "pages.xray.routing.refuse.exitMissing"
	KeyNoExitDriver         = "pages.xray.routing.refuse.noExitDriver"
	KeyNoSnat               = "pages.xray.routing.refuse.noSnat"
	KeyNoDefaultOutbound    = "pages.xray.routing.refuse.noDefaultOutbound"
	KeyExitTCPOnly          = "pages.xray.routing.refuse.exitTcpOnly"
	KeyExitTCPNarrowed      = "pages.xray.routing.warn.exitTcpNarrowed"
	KeyExitContained        = "pages.xray.routing.warn.exitContained"
)

// Refusals returns the diags that must block a save. A preview shows every diag;
// only these stop the write, because an inert rule that is merely advisory would
// train an operator to ignore the one that matters.
// Warnings are the diags of rules that apply with a limitation the operator
// should see: a refusal blocks a save, a warning must still reach the list.
func (c Compiled) Warnings() []Diag {
	var out []Diag
	for _, diag := range c.Diags {
		if diag.Severity == SeverityWarn {
			out = append(out, diag)
		}
	}
	return out
}

func (c Compiled) Refusals() []Diag {
	var out []Diag
	for _, diag := range c.Diags {
		if diag.Severity == SeverityInert || diag.Severity == SeverityBlocked {
			out = append(out, diag)
		}
	}
	return out
}
