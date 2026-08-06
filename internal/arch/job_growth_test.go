package arch

import (
	"go/ast"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"unicode"
)

/*
Per-core cron job ratchet — docs/multi-core-architecture.md §10.

The shape it exists to prevent is 11 cores x 3 cron jobs: supervision, traffic
and upkeep written once per core because nothing stopped the copy-paste. A
registration is per-core when its cadence constant or its job argument NAMES a
core — the two identifiers a copied job carries with it.

Budgets are exact in both directions, like the dispatch ratchet: a core missing
from the map may have no jobs at all, and removing the last of a budgeted core's
jobs must lower its number in the same commit.
*/
var perCoreJobBudget = map[string]int{
	/*
		All four predate the registry and all four are Xray's alone: a 1s
		liveness check, a 30s pending-restart applier, the traffic job that kept
		its name when it started billing every core, and log pruning. They are
		the residue this guard measures, not a licence for a fifth.
	*/
	"xray": 4,
}

func TestJobCountDoesNotGrowPerCore(t *testing.T) {
	root := repoRoot(t)
	names := coreNames(t, root)

	counts := map[string]int{}
	sites := map[string][]string{}
	registrations := 0
	for _, src := range parseNonTestGo(t, root) {
		ast.Inspect(src.File, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !isCronRegistration(call) {
				return true
			}
			registrations++
			for _, named := range coresNamedBy(call, names) {
				counts[named]++
				position := src.Fset.Position(call.Pos())
				sites[named] = append(sites[named], src.Rel+":"+strconv.Itoa(position.Line))
			}
			return true
		})
	}

	// Guard the guard: with no registrations found the detector has stopped
	// matching the scheduler, and every budget below would read green.
	if registrations == 0 {
		t.Fatal("found no cron registrations; the scheduler's AddJob/AddFunc calls moved or were renamed and this guard is now vacuous")
	}

	for name, budget := range perCoreJobBudget {
		if counts[name] != budget {
			t.Errorf("core %q has %d cron jobs, budget %d: %v\nlower the budget in this commit when you remove one; raise it only if a core genuinely cannot be supervised through the registry",
				name, counts[name], budget, sites[name])
		}
	}
	for _, name := range sortedKeys(counts) {
		if _, budgeted := perCoreJobBudget[name]; budgeted {
			continue
		}
		t.Errorf("core %q has %d cron jobs of its own: %v\na core is supervised, billed and reaped because it is REGISTERED — drive it off core.Registry instead of scheduling a job for it",
			name, counts[name], sites[name])
	}
}

// coreNames is the vocabulary a job is checked against: every registered core
// (one directory under internal/cores/internal) and every protocol constant.
func coreNames(t *testing.T, root string) map[string]bool {
	t.Helper()
	out := map[string]bool{}
	for _, value := range protocolConstants(t, root) {
		out[strings.ToLower(value)] = true
	}
	entries, err := os.ReadDir(filepath.Join(root, "internal", "cores", "internal"))
	if err != nil {
		t.Fatalf("read the core packages: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			out[strings.ToLower(entry.Name())] = true
		}
	}
	if len(out) == 0 {
		t.Fatal("found no core names; internal/cores/internal moved and this guard would pass on any job")
	}
	return out
}

func isCronRegistration(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	return ok && (sel.Sel.Name == "AddJob" || sel.Sel.Name == "AddFunc")
}

/*
coresNamedBy returns the cores a registration names, once each.

Function-literal bodies are deliberately not read: what a job DOES touches a
core by definition, while a per-core job is one whose cadence or constructor is
named after a core. Reading bodies would flag the shared status ticker for
sampling Xray metrics, which is one job no matter how many cores exist.
*/
func coresNamedBy(call *ast.CallExpr, names map[string]bool) []string {
	found := map[string]bool{}
	for _, arg := range call.Args {
		ast.Inspect(arg, func(n ast.Node) bool {
			if _, isFuncLit := n.(*ast.FuncLit); isFuncLit {
				return false
			}
			ident, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			for _, word := range splitIdentifier(ident.Name) {
				if names[word] {
					found[word] = true
				}
			}
			return true
		})
	}
	return sortedKeys(found)
}

// splitIdentifier lowercases the camel-case words of an identifier, so both
// cadenceMtproto and NewMtprotoJob yield "mtproto".
func splitIdentifier(name string) []string {
	var words []string
	var current strings.Builder
	for _, r := range name {
		if unicode.IsUpper(r) || r == '_' {
			if current.Len() > 0 {
				words = append(words, strings.ToLower(current.String()))
				current.Reset()
			}
			if r == '_' {
				continue
			}
		}
		current.WriteRune(r)
	}
	if current.Len() > 0 {
		words = append(words, strings.ToLower(current.String()))
	}
	return words
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
