package arch

import (
	"go/ast"
	"testing"
)

/*
The panel builds exactly one runtime.LocalDeps, in internal/web/web.go, and
every field on it is optional at compile time.

That is deliberate — a runtime test builds a Local without a database — but it
means dropping a field from the production literal degrades silently rather than
failing to build. RenderInbound is the sharp one: without it a hot apply falls
back to the stored sections, so an inbound edited under load keeps
quota-exhausted clients and loses its fallbacks, while every test that wires its
own deps still passes.

Add a field here when a new one is load-bearing; leave the optional ones out.
*/
var requiredLocalDeps = []string{"Cores", "RenderInbound"}

func TestLocalRuntimeIsWiredToRender(t *testing.T) {
	root := repoRoot(t)
	found := 0
	for _, src := range parseNonTestGo(t, root) {
		ast.Inspect(src.File, func(node ast.Node) bool {
			lit, ok := node.(*ast.CompositeLit)
			if !ok {
				return true
			}
			selector, ok := lit.Type.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "LocalDeps" {
				return true
			}
			ident, ok := selector.X.(*ast.Ident)
			if !ok || ident.Name != "runtime" {
				return true
			}
			found++
			set := map[string]bool{}
			for _, element := range lit.Elts {
				kv, ok := element.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if key, ok := kv.Key.(*ast.Ident); ok {
					set[key.Name] = true
				}
			}
			for _, field := range requiredLocalDeps {
				if !set[field] {
					t.Errorf("%s builds runtime.LocalDeps without %s; the field is optional at compile time, so the panel would silently run degraded", src.Rel, field)
				}
			}
			return true
		})
	}
	if found == 0 {
		t.Fatal("found no runtime.LocalDeps literal outside tests; the panel's wiring moved and this guard is vacuous")
	}
}
