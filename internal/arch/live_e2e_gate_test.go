package arch

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

/*
The live suites write real kernel state: pwg<N> devices, ip rules and routing
tables in the reserved band. Their gate is what keeps that inside a namespace,
so a TestLive* that forgets to call it is the whole hazard, not a style slip.
*/

// The other guards parse non-test files on purpose; these ARE test files, so
// this one reads them directly rather than widening the shared scan.
var liveGateFiles = map[string]string{
	"internal/wireguard/e2e_linux_test.go": "e2e",
	"internal/egress/e2e_linux_test.go":    "e2e",
}

func TestEveryLiveTestCallsItsGate(t *testing.T) {
	root := repoRoot(t)
	for rel, gate := range liveGateFiles {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filepath.Join(root, filepath.FromSlash(rel)), nil, 0)
		if err != nil {
			t.Fatalf("%s: %v — renamed or deleted, so its gate is no longer pinned", rel, err)
		}
		bodies := map[string]*ast.BlockStmt{}
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Body != nil && fn.Recv == nil {
				bodies[fn.Name.Name] = fn.Body
			}
		}
		var live int
		for name, body := range bodies {
			if !strings.HasPrefix(name, "TestLive") {
				continue
			}
			live++
			if !reachesGate(body, gate, bodies, map[string]bool{}) {
				t.Errorf("%s: %s never reaches %s(t) — it would write kernel state on whatever host runs it",
					rel, name, gate)
			}
		}
		if live == 0 {
			t.Errorf("%s: no TestLive* found; the guard is watching a file that no longer holds the live suite", rel)
		}
	}
}

// reachesGate follows calls into helpers declared in the same file, so a test
// that gates through liveManager still counts. Resolving rather than trusting a
// name prefix is the point: otherwise renaming a helper past the check disarms it.
func reachesGate(body *ast.BlockStmt, gate string, bodies map[string]*ast.BlockStmt, seen map[string]bool) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || found {
			return !found
		}
		id, ok := call.Fun.(*ast.Ident)
		if !ok {
			return true
		}
		if id.Name == gate {
			found = true
			return false
		}
		if inner, ok := bodies[id.Name]; ok && !seen[id.Name] {
			seen[id.Name] = true
			if reachesGate(inner, gate, bodies, seen) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}
