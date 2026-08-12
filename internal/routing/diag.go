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
const (
	KeyNoFront              = "pages.xray.routing.refuse.noFront"
	KeyCriterionUnsupported = "pages.xray.routing.refuse.criterionUnsupported"
	KeyExitMissing          = "pages.xray.routing.refuse.exitMissing"
	KeyNoExitDriver         = "pages.xray.routing.refuse.noExitDriver"
	KeyNoSnat               = "pages.xray.routing.refuse.noSnat"
	KeySelfLoop             = "pages.xray.routing.refuse.selfLoop"
)

// Refusals returns the diags that must block a save. A preview shows every diag;
// only these stop the write, because an inert rule that is merely advisory would
// train an operator to ignore the one that matters.
func (c Compiled) Refusals() []Diag {
	var out []Diag
	for _, diag := range c.Diags {
		if diag.Severity == SeverityInert || diag.Severity == SeverityBlocked {
			out = append(out, diag)
		}
	}
	return out
}
