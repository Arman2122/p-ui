package arch

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

/*
Shared helpers for the architecture guards in this package.

These tests read the repository as source text rather than importing it, so they
run on any OS and need neither a database nor a built frontend. That matters:
the guards must stay runnable in an editor on a workstation that cannot compile
the Linux-only packages they are guarding.
*/

// goSource is one parsed non-test file, keyed by its slash-separated path
// relative to the repo root so budgets and messages are platform-stable.
type goSource struct {
	Rel  string
	File *ast.File
	Fset *token.FileSet
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod found above the test's working directory; cannot locate the repo root")
		}
		dir = parent
	}
}

// scanRoots are the trees the guards police: all backend Go plus the entry point.
func scanRoots(root string) []string {
	return []string{filepath.Join(root, "internal"), filepath.Join(root, "main.go")}
}

// parseNonTestGo returns every non-test .go file under the scan roots. Test files
// are excluded throughout: a table-driven test naming every protocol is correct.
func parseNonTestGo(t *testing.T, root string) []goSource {
	t.Helper()
	fset := token.NewFileSet()
	var out []goSource
	for _, scanRoot := range scanRoots(root) {
		err := filepath.WalkDir(scanRoot, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if d.Name() == "dist" || d.Name() == "node_modules" {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			file, parseErr := parser.ParseFile(fset, path, nil, 0)
			if parseErr != nil {
				return parseErr
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			out = append(out, goSource{Rel: filepath.ToSlash(rel), File: file, Fset: fset})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", scanRoot, err)
		}
	}
	if len(out) == 0 {
		t.Fatal("parsed no Go files; the scan roots are wrong and every guard in this package is vacuous")
	}
	return out
}

// importPaths returns the unquoted import paths of a parsed file.
func importPaths(file *ast.File) []string {
	out := make([]string, 0, len(file.Imports))
	for _, spec := range file.Imports {
		out = append(out, strings.Trim(spec.Path.Value, `"`))
	}
	return out
}

// protocolConstants returns the names and values of the model.Protocol const
// block. It is the single source the other guards compare everything else against.
func protocolConstants(t *testing.T, root string) map[string]string {
	t.Helper()
	path := filepath.Join(root, "internal", "database", "model", "model.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse model.go: %v", err)
	}
	out := map[string]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			ident, ok := vs.Type.(*ast.Ident)
			if !ok || ident.Name != "Protocol" {
				continue
			}
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				out[name.Name] = strings.Trim(lit.Value, `"`)
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("found no model.Protocol constants; the const block moved or was renamed and every protocol guard is now vacuous")
	}
	return out
}

// structFields returns the declared field names of a named struct in model.go.
func structFields(t *testing.T, root, typeName string) []string {
	t.Helper()
	path := filepath.Join(root, "internal", "database", "model", "model.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse model.go: %v", err)
	}
	var fields []string
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != typeName {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range st.Fields.List {
				for _, name := range field.Names {
					fields = append(fields, name.Name)
				}
			}
		}
	}
	if len(fields) == 0 {
		t.Fatalf("found no fields on %s; it was renamed or moved and its column guard is now vacuous", typeName)
	}
	return fields
}
