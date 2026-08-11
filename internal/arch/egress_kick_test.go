package arch

import (
	"go/ast"
	"sort"
	"testing"
)

/*
Every path that leaves a new core config live must kick the egress reconcile.

The `default dev peg<id>` route needs a front device the core owns: a restart
destroys it, a hot apply creates it, and the kernel restores the route in
neither case. A path that forgets the kick leaves every attached inbound
contained — not leaking, but dark — until the 10s drift tick, and RestartXray
returns early on a successful hot apply, which is how that path lost it once.

Budgeted in both directions like the dispatch ratchet: a new caller is argued
for here, and deleting the last one lowers the list in the same commit.
*/

// kickFunc is the reconcile kick and coreSwapFile the file that owns both swaps.
const (
	kickFunc     = "kickEgressAfterCoreRestart"
	coreSwapFile = "internal/web/service/xray.go"
)

// kickCallers is every function that concludes a core config swap, one entry per
// call: RestartXray after the new process starts, tryHotApply after the diff lands.
var kickCallers = []string{
	coreSwapFile + ":RestartXray",
	coreSwapFile + ":tryHotApply",
}

func TestEgressKickCoversEveryCoreConfigSwap(t *testing.T) {
	root := repoRoot(t)
	var callers []string
	seenSwapFile := false
	for _, src := range parseNonTestGo(t, root) {
		if src.Rel == coreSwapFile {
			seenSwapFile = true
		}
		for _, decl := range src.File.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == kickFunc {
					callers = append(callers, src.Rel+":"+fn.Name.Name)
				}
				return true
			})
		}
	}
	if !seenSwapFile {
		t.Fatalf("%s was not parsed; it moved or was renamed and this guard is now vacuous", coreSwapFile)
	}

	want := append([]string(nil), kickCallers...)
	sort.Strings(want)
	sort.Strings(callers)
	if len(callers) != len(want) {
		t.Fatalf("%s is called from %v, want exactly %v", kickFunc, callers, want)
	}
	for i := range want {
		if callers[i] != want[i] {
			t.Fatalf("%s is called from %v, want exactly %v", kickFunc, callers, want)
		}
	}
}
