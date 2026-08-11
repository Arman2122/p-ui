package arch

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strings"
	"testing"
)

// policedPackages must answer every product question without naming a core.
// internal/shaping lands in a later step: absence is fine, emptiness is not.
var policedPackages = []string{"internal/policy/", "internal/shaping/"}

func isPoliced(rel string) bool {
	for _, prefix := range policedPackages {
		if strings.HasPrefix(rel, prefix) {
			return true
		}
	}
	return false
}

// namesCoreKind reports whether an expression reads a protocol or a core kind.
// The string() unwrap is the shape the dispatch ratchet had to learn once already.
func namesCoreKind(expr string) bool {
	if inner, ok := strings.CutPrefix(expr, "string("); ok {
		expr = strings.TrimSuffix(inner, ")")
	}
	return strings.HasSuffix(expr, ".Protocol") || strings.HasSuffix(expr, ".Kind")
}

// TestPolicyNamesNoProtocol seeds at zero while it is still cheap. Two shapes are
// new: the ratchet needs a `.Protocol` suffix, so .Kind and a bare kind count zero there.
func TestPolicyNamesNoProtocol(t *testing.T) {
	root := repoRoot(t)
	consts := protocolConstants(t, root)
	kindValues := make(map[string]bool, len(consts))
	for _, value := range consts {
		kindValues[value] = true
	}

	var violations []string
	checked := 0
	for _, src := range parseNonTestGo(t, root) {
		if !isPoliced(src.Rel) {
			continue
		}
		checked++
		// Relative path, like every other guard here: an absolute one differs per
		// machine and per OS, and these messages are read in CI logs.
		report := func(pos token.Pos, what string) {
			at := src.Fset.Position(pos)
			violations = append(violations, fmt.Sprintf("%s:%d:%d: %s", src.Rel, at.Line, at.Column, what))
		}
		ast.Inspect(src.File, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.SelectorExpr:
				pkg, ok := node.X.(*ast.Ident)
				if ok && pkg.Name == "model" {
					if _, isProtocol := consts[node.Sel.Name]; isProtocol {
						report(node.Pos(), "model."+node.Sel.Name)
					}
				}
			case *ast.BinaryExpr:
				if node.Op != token.EQL && node.Op != token.NEQ {
					return true
				}
				if namesCoreKind(types.ExprString(node.X)) || namesCoreKind(types.ExprString(node.Y)) {
					report(node.Pos(), types.ExprString(node))
				}
			case *ast.SwitchStmt:
				if node.Tag != nil && namesCoreKind(types.ExprString(node.Tag)) {
					report(node.Pos(), "switch "+types.ExprString(node.Tag))
				}
			case *ast.BasicLit:
				if node.Kind == token.STRING && kindValues[strings.Trim(node.Value, `"`)] {
					report(node.Pos(), "the literal "+node.Value)
				}
			}
			return true
		})
	}

	if checked == 0 {
		t.Fatalf("parsed no files under %v; this guard is certifying nothing", policedPackages)
	}
	sort.Strings(violations)
	for _, v := range violations {
		t.Errorf("%s — these packages decide product rules and drive the kernel; ask the registry what a kind can do instead of naming one here", v)
	}
}
