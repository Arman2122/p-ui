package arch

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"testing"
)

/*
Capability interfaces may be type-asserted in exactly one file.

Segregating the core contract into optional interfaces does not by itself remove
protocol dispatch — it relocates it. `if h, ok := c.(HotApplier); ok` scattered
through the service layer is the same coupling as `switch inbound.Protocol`, only
harder to find. internal/core/bind.go resolves every capability once into a struct
of nil-able fields; everywhere else checks a field.

Seeded at zero violations, so it can only ever be broken deliberately.
*/

// bindFile is the only file allowed to assert a capability interface.
const bindFile = "internal/core/bind.go"

// capabilityInterfaces reads the names of the optional interfaces from caps.go
// so this guard cannot fall behind a newly added capability.
func capabilityInterfaces(t *testing.T, root string) map[string]bool {
	t.Helper()
	path := filepath.Join(root, "internal", "core", "caps.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse caps.go: %v", err)
	}
	names := map[string]bool{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if _, isInterface := ts.Type.(*ast.InterfaceType); isInterface {
				names[ts.Name.Name] = true
			}
		}
	}
	if len(names) == 0 {
		t.Fatal("found no interfaces in internal/core/caps.go; the capabilities moved and this guard is vacuous")
	}
	return names
}

// assertedTypeName returns the name of the type in a type assertion, whether it
// is written bare inside package core or qualified as core.X elsewhere.
func assertedTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		if pkg, ok := t.X.(*ast.Ident); ok && pkg.Name == "core" {
			return t.Sel.Name
		}
	}
	return ""
}

func TestCapabilityAssertionsOnlyInBind(t *testing.T) {
	root := repoRoot(t)
	capabilities := capabilityInterfaces(t, root)

	var violations []string
	for _, src := range parseNonTestGo(t, root) {
		if src.Rel == bindFile {
			continue
		}
		ast.Inspect(src.File, func(n ast.Node) bool {
			assert, ok := n.(*ast.TypeAssertExpr)
			// A nil Type is `x.(type)` in a type switch, which has no asserted type.
			if !ok || assert.Type == nil {
				return true
			}
			if name := assertedTypeName(assert.Type); capabilities[name] {
				violations = append(violations,
					src.Rel+":"+strconv.Itoa(src.Fset.Position(assert.Pos()).Line)+" asserts core."+name)
			}
			return true
		})
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Errorf("%s — capabilities are resolved once in %s; read the matching field on core.Bound instead, or this becomes the new switch on protocol", v, bindFile)
	}
}
