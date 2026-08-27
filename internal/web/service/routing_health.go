package service

import (
	"sync"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/routing"
)

/*
Where a rule that went inert AFTER it was saved becomes visible.

Saving a rule the compiler would drop is refused (F049). The ones this file is
about are the rules that were fine when saved and stopped being routable later:
the inbound they name was disabled, the exit stopped resolving, the Xray
template was edited out from under them. Those arrive after the only check that
existed, and until now the only trace was a log line nobody watches.

Generate time is the only place they can be found. It is also a place with no
HTTP response to attach a warning to -- generation happens on restart -- so the
last generate's refusals are held here and served on the rules list the Routing
page already polls. Ledger L8.

A snapshot, not a store: it describes the running config, so it is rebuilt every
generate and lost on restart, which is correct. A restart regenerates.
*/
var lastRoutingRefusals struct {
	sync.RWMutex
	byRule map[int][]routing.Diag
}

// recordRoutingRefusals replaces the snapshot with this generate's refusals.
// Called with the full set, including an empty one -- a rule that came back is
// as much news as one that went inert.
func recordRoutingRefusals(diags []routing.Diag) {
	byRule := make(map[int][]routing.Diag, len(diags))
	for _, diag := range diags {
		byRule[diag.RuleID] = append(byRule[diag.RuleID], diag)
	}
	lastRoutingRefusals.Lock()
	lastRoutingRefusals.byRule = byRule
	lastRoutingRefusals.Unlock()
}

func routingRefusalsFor(ruleID int) []routing.Diag {
	lastRoutingRefusals.RLock()
	defer lastRoutingRefusals.RUnlock()
	return lastRoutingRefusals.byRule[ruleID]
}

/*
RuleView is a stored rule plus what the last config generation made of it.

The rule is embedded rather than nested so the list keeps the shape it has
always had and a client that ignores diags is unaffected.
*/
type RuleView struct {
	// By value, not by pointer: openapigen promotes an embedded struct's fields
	// into the schema only for a value embed, and a pointer one would document
	// the response as a nested object it is not.
	model.RoutingRule
	// Diags carries the refusals for this rule from the last generate, one per
	// (rule, subject) pair. Empty means the rule is realised, or that no config
	// has been generated since this process started.
	Diags []routing.Diag `json:"diags,omitempty" example:"[]"`
}

// RuleViews is Rules with each rule's last-generate refusals attached.
func (s *RoutingService) RuleViews() ([]RuleView, error) {
	rules, err := s.Rules()
	if err != nil {
		return nil, err
	}
	views := make([]RuleView, 0, len(rules))
	for _, rule := range rules {
		views = append(views, RuleView{RoutingRule: *rule, Diags: routingRefusalsFor(rule.Id)})
	}
	return views, nil
}
