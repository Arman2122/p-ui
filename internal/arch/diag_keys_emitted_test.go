package arch

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

/*
A declared message key must be emitted by something.

KeySelfLoop sat in internal/routing/diag.go from e30b1841 with no emit site
anywhere, and nothing in the build could see it. The i18n dead-keys scan walks
the LOCALE file, so a key with no shipped string is invisible to it in both
directions — which is exactly what a declared-and-never-built diagnostic looks
like. Ledger L6.

This is the check that was missing: the constants are the vocabulary, so each
one has to be used somewhere other than where it is declared.
*/
func TestEveryDeclaredDiagKeyIsEmitted(t *testing.T) {
	root := repoRoot(t)
	diagPath := filepath.Join(root, "internal", "routing", "diag.go")

	declared := diagKeyConstants(t, diagPath)
	if len(declared) == 0 {
		t.Fatal("no Key* constants found in internal/routing/diag.go; this guard is now vacuous")
	}

	used := map[string]bool{}
	// Tests are excluded, this file included: a key named only by a test — or by
	// the prose of the guard that checks it — is not an emit site. Caught the
	// first version of this guard, which passed because it quoted the key it was
	// looking for.
	walkGoSources(t, filepath.Join(root, "internal"), func(path string, src string) {
		if path == diagPath || strings.HasSuffix(path, "_test.go") {
			return
		}
		for name := range declared {
			if strings.Contains(src, name) {
				used[name] = true
			}
		}
	})

	for name, key := range declared {
		if !used[name] {
			t.Fatalf("%s (%s) is declared and emitted nowhere: a diagnostic the compiler can never produce, and one the locale scan cannot see because it has no shipped string", name, key)
		}
	}
}

// diagKeyConstants returns the Key* constants declared in one file, name to value.
func diagKeyConstants(t *testing.T, path string) map[string]string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	out := map[string]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range value.Names {
				if !strings.HasPrefix(name.Name, "Key") || i >= len(value.Values) {
					continue
				}
				lit, ok := value.Values[i].(*ast.BasicLit)
				if ok && lit.Kind == token.STRING {
					out[name.Name] = strings.Trim(lit.Value, `"`)
				}
			}
		}
	}
	return out
}

func walkGoSources(t *testing.T, dir string, visit func(path, src string)) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") {
			return err
		}
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		visit(path, string(body))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
}
