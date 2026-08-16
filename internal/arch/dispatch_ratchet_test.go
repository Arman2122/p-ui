package arch

import (
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"
	"testing"
)

/*
Protocol dispatch ratchet.

model.Protocol is a bare string with no behaviour, so every place that needs to
know what a protocol *does* re-derives it by hand. That is the cost driver for
adding a new core: each site is one more thing the next core's author must find
and extend. The multi-core refactor moves these onto a core registry, and this
ratchet exists so they cannot grow back while that work is in flight.

The budget is a measurement, not a target, and it is checked in BOTH directions.
Failing only upward lets slack accumulate every time a site is deleted, and the
guard dies quietly; failing downward forces whoever removed a site to lower the
number in the same PR. TestRouteRegistryContract uses the same two-way shape.

Detected, in non-test Go under internal/ and main.go:
  - a `model.<Const>` selector naming a declared Protocol constant
  - a string literal compared against an expression ending in `.Protocol`
  - a literal `case` arm of a `switch` on such an expression

That third shape was missed until 2026-08-06, and it is how the largest tables
in the tree are written — sub/service.go's share-link dispatch among them. Adding
it moved the total from 94 to 123 with nothing having regressed: 29 sites had
always been there, and the guard was reporting a number 23% smaller than the
thing it was guarding.

Not detected: comparisons against a bare local named `protocol`. Requiring the
field selector keeps the Xray *outbound* protocol namespace (freedom, blackhole,
dns, loopback in service/outbound/) out of a budget about *inbound* core kinds.
*/

// dispatchBudget is the measured count per file. Lower it as sites migrate to
// the registry; never raise it without agreement that a new core needs the site.
var dispatchBudget = map[string]int{
	"internal/web/service/inbound.go":              12,
	"internal/web/service/client_inbound_apply.go": 7,
	"internal/web/service/xray.go":                 9,
	"internal/web/service/inbound_clients.go":      8,
	"internal/web/service/tgbot/tgbot_inbound.go":  10,
	"internal/sub/service.go":                      16,
	"internal/sub/clash_service.go":                7,
	"internal/database/db.go":                      6,
	"internal/web/service/inbound_protocol.go":     1,
	"internal/web/service/inbound_mtproto.go":      3,
	"internal/sub/json_service.go":                 9,
	"internal/web/service/inbound_migration.go":    2,
	"internal/mtproto/manager.go":                  1,
	"internal/web/service/inbound_flow_restore.go": 1,
	"internal/web/service/inbound_traffic.go":      1,
}

// dispatchTotal guards the guard. If the detector stops matching the code — a
// rename of Protocol, a moved const block — every per-file budget silently goes
// green, and only a total that must still be met catches it.
const dispatchTotal = 93

// frozenDispatch are sites that must NOT migrate to the registry. Historical
// migrations are frozen facts about data written by past releases; rewriting
// them through a registry would change what they do to old rows.
var frozenDispatch = map[string]string{
	"internal/database/db.go":                   "one-off data migrations for shipped releases",
	"internal/web/service/inbound_migration.go": "one-off data migrations for shipped releases",
	"internal/mtproto/manager.go":               "a core naming its own kind, which stays correct after the refactor",
}

/*
dispatchesOnProtocol reports whether an expression reads an inbound's protocol.

The string() wrapper is unwrapped because it defeats a plain suffix test, and
that is exactly how one comparison in check_client_ip_job.go was written — a
site the guard could not see for as long as it existed.
*/
func dispatchesOnProtocol(expr string) bool {
	if inner, ok := strings.CutPrefix(expr, "string("); ok {
		expr = strings.TrimSuffix(inner, ")")
	}
	return strings.HasSuffix(expr, ".Protocol")
}

func countDispatchSites(t *testing.T, sources []goSource, consts map[string]string) map[string]int {
	t.Helper()
	counts := map[string]int{}
	for _, src := range sources {
		ast.Inspect(src.File, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.SelectorExpr:
				pkg, ok := node.X.(*ast.Ident)
				if ok && pkg.Name == "model" {
					if _, isProtocol := consts[node.Sel.Name]; isProtocol {
						counts[src.Rel]++
					}
				}
			case *ast.BinaryExpr:
				if node.Op != token.EQL && node.Op != token.NEQ {
					return true
				}
				lit, isLit := node.Y.(*ast.BasicLit)
				other := node.X
				if !isLit {
					lit, isLit = node.X.(*ast.BasicLit)
					other = node.Y
				}
				if !isLit || lit.Kind != token.STRING {
					return true
				}
				if dispatchesOnProtocol(types.ExprString(other)) {
					counts[src.Rel]++
				}
			case *ast.SwitchStmt:
				if node.Tag == nil || !dispatchesOnProtocol(types.ExprString(node.Tag)) {
					return true
				}
				// Literal arms only: a model.<Const> arm is already counted by
				// the SelectorExpr case, which Inspect reaches on its own.
				for _, stmt := range node.Body.List {
					clause, ok := stmt.(*ast.CaseClause)
					if !ok {
						continue
					}
					for _, expr := range clause.List {
						if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.STRING {
							counts[src.Rel]++
						}
					}
				}
			}
			return true
		})
	}
	return counts
}

func TestProtocolDispatchRatchet(t *testing.T) {
	root := repoRoot(t)
	consts := protocolConstants(t, root)
	counts := countDispatchSites(t, parseNonTestGo(t, root), consts)

	total := 0
	for _, n := range counts {
		total += n
	}
	if total == 0 {
		t.Fatal("detected zero protocol dispatch sites; the detector no longer matches the code and this ratchet is certifying nothing")
	}

	files := make([]string, 0, len(counts)+len(dispatchBudget))
	seen := map[string]bool{}
	for f := range counts {
		if !seen[f] {
			files = append(files, f)
			seen[f] = true
		}
	}
	for f := range dispatchBudget {
		if !seen[f] {
			files = append(files, f)
			seen[f] = true
		}
	}
	sort.Strings(files)

	t.Run("no file gains dispatch sites", func(t *testing.T) {
		for _, f := range files {
			if got, want := counts[f], dispatchBudget[f]; got > want {
				t.Errorf("%s has %d protocol dispatch sites, budget is %d — route the new behaviour through the core registry instead of switching on the protocol (see docs/multi-core-architecture.md)", f, got, want)
			}
		}
	})

	t.Run("budgets are lowered when sites are removed", func(t *testing.T) {
		for _, f := range files {
			if got, want := counts[f], dispatchBudget[f]; got < want {
				t.Errorf("%s has %d protocol dispatch sites but the budget still says %d — lower it in this PR, or the ratchet accumulates slack and stops guarding anything", f, got, want)
			}
		}
	})

	t.Run("total matches", func(t *testing.T) {
		if total != dispatchTotal {
			t.Errorf("counted %d dispatch sites in total, dispatchTotal says %d — update it together with the per-file budget", total, dispatchTotal)
		}
	})

	t.Run("frozen sites are still budgeted", func(t *testing.T) {
		for f, why := range frozenDispatch {
			if dispatchBudget[f] == 0 {
				t.Errorf("%s is listed as frozen (%s) but has no budget entry — either it moved, or it was migrated when it should not have been", f, why)
			}
		}
	})
}
