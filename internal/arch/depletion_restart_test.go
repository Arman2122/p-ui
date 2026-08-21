package arch

import (
	"go/ast"
	"testing"
)

/*
restartXrayOnClientDisable has to force the restart, in every path that honours it.

Measured against the shipped core: RemoveUser deletes a map entry and closes
nothing, so a session that is already up keeps flowing, and a client whose mux
connection is warm keeps opening new requests over it. Removing the whole inbound
does not cut them either — the worker closes its listener and never tracks
accepted connections. Replacing the process is the only lever the core exposes.

So RestartXray(false) here is indistinguishable from doing nothing: it reconciles,
the hot apply carries the user removal over the core API, and the process is never
replaced. The setting reads as enforced, and the depleted client keeps its tunnel
until it disconnects on its own.

The two paths disagreed for exactly this reason, which is what this guard exists
to stop happening again.
*/
func TestDepletionRestartIsForcedEverywhereItIsHonoured(t *testing.T) {
	paths := []string{
		"internal/web/job/xray_traffic_job.go",
		"internal/web/job/node_traffic_sync_job.go",
	}

	sources := map[string]*ast.File{}
	for _, src := range parseNonTestGo(t, repoRoot(t)) {
		sources[src.Rel] = src.File
	}

	checked := 0
	for _, rel := range paths {
		file, ok := sources[rel]
		if !ok {
			t.Fatalf("%s was not parsed; it moved or was renamed and this guard is now vacuous", rel)
		}
		ast.Inspect(file, func(node ast.Node) bool {
			ifStmt, ok := node.(*ast.IfStmt)
			if !ok || !mentions(ifStmt.Cond, "restartOnDisable") {
				return true
			}
			ast.Inspect(ifStmt.Body, func(inner ast.Node) bool {
				call, ok := inner.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "RestartXray" || len(call.Args) != 1 {
					return true
				}
				checked++
				arg, ok := call.Args[0].(*ast.Ident)
				if !ok || arg.Name != "true" {
					t.Errorf("%s honours restartXrayOnClientDisable with RestartXray(%s): a reconcile hot-applies the removal and never replaces the process, so the depleted client keeps the connection it already has",
						rel, exprName(call.Args[0]))
				}
				return true
			})
			return true
		})
	}

	if checked != len(paths) {
		t.Fatalf("found %d guarded RestartXray call(s) across %v, want one per path; the branch moved and this guard is now vacuous",
			checked, paths)
	}
}

// mentions reports whether an identifier appears anywhere in an expression, so
// the guard finds the branch by what it tests rather than by its shape.
func mentions(expr ast.Expr, name string) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		if ident, ok := node.(*ast.Ident); ok && ident.Name == name {
			found = true
		}
		return !found
	})
	return found
}

func exprName(expr ast.Expr) string {
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return "a non-literal argument"
}
