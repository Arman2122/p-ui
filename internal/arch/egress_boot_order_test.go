package arch

import (
	"go/ast"
	"go/token"
	"testing"
)

/*
Boot is fail-closed by ORDER, and nothing else makes it so.

An egress installs its blackhole and its rules with the front device absent and
reattaches when the core creates it, so a pass that runs before any core starts
leaves every attached inbound contained. Run it after, and there is a window in
which the front exists, the rules do not, and the inbound egresses with the
server's own address -- the one outcome the whole feature exists to prevent.

The pass must also be synchronous. `go egressJob.Run()` would type-check, read
as a harmless speedup and reopen exactly the same window.
*/

// The three calls whose order is the invariant, named as they appear in the
// scheduler: the kick, the core start it must precede, and the supervisor.
const (
	bootFile      = "internal/web/web.go"
	bootFunc      = "startTask"
	egressKick    = "egressJob"
	superviseKick = "superviseJob"
	coreStart     = "RestartXray"
)

func TestEgressContainmentIsInstalledBeforeAnyCoreStarts(t *testing.T) {
	body := funcBody(t, bootFile, bootFunc)

	var egressAt, superviseAt, coreAt []token.Pos
	backgrounded := map[token.Pos]bool{}
	ast.Inspect(body, func(node ast.Node) bool {
		if goStmt, ok := node.(*ast.GoStmt); ok {
			backgrounded[goStmt.Call.Pos()] = true
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		receiver, _ := sel.X.(*ast.Ident)
		switch {
		case sel.Sel.Name == coreStart:
			coreAt = append(coreAt, call.Pos())
		case sel.Sel.Name != "Run" || receiver == nil:
		case receiver.Name == egressKick:
			egressAt = append(egressAt, call.Pos())
		case receiver.Name == superviseKick:
			superviseAt = append(superviseAt, call.Pos())
		}
		return true
	})

	if len(egressAt) != 1 {
		t.Fatalf("%s.%s calls %s.Run() %d times, want exactly 1: boot fail-closed is decided by that one call's position",
			bootFile, bootFunc, egressKick, len(egressAt))
	}
	for _, other := range []struct {
		name string
		at   []token.Pos
	}{{coreStart, coreAt}, {superviseKick + ".Run", superviseAt}} {
		if len(other.at) == 0 {
			t.Fatalf("%s.%s no longer calls %s; this guard is now vacuous", bootFile, bootFunc, other.name)
		}
		for _, at := range other.at {
			if at < egressAt[0] {
				t.Fatalf("%s starts before %s.Run(): an attached inbound egresses with the server's own address until the first drift tick",
					other.name, egressKick)
			}
		}
	}
	if backgrounded[egressAt[0]] {
		t.Fatalf("%s.Run() runs in a goroutine, so nothing orders it against %s", egressKick, coreStart)
	}
}

// The job behind the guard above. If Run stops converging, the ordered call at
// boot and the 10s drift tick both become no-ops and nothing else notices.
func TestTheEgressJobConverges(t *testing.T) {
	body := funcBody(t, "internal/web/job/egress_reconcile.go", "Run")
	found := false
	ast.Inspect(body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Reconcile" {
			found = true
		}
		return true
	})
	if !found {
		t.Fatal("EgressReconcileJob.Run calls no Reconcile: the boot pass and the drift tick both converge nothing")
	}
}

// funcBody returns the body of one top-level or method declaration, failing
// rather than returning nil so a rename cannot leave a guard silently green.
func funcBody(t *testing.T, rel, name string) *ast.BlockStmt {
	t.Helper()
	for _, src := range parseNonTestGo(t, repoRoot(t)) {
		if src.Rel != rel {
			continue
		}
		for _, decl := range src.File.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && fn.Name.Name == name && fn.Body != nil {
				return fn.Body
			}
		}
		t.Fatalf("%s declares no %s; it moved or was renamed and its guard is now vacuous", rel, name)
	}
	t.Fatalf("%s was not parsed; it moved or was renamed and its guard is now vacuous", rel)
	return nil
}
