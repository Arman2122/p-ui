package arch

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

/*
Every model with a database-side boolean default owes a proof that it can still
be created false.

GORM omits a zero-valued field whose column carries a `default`, and then writes
the default back into the struct — so a row created with its switch off is
stored ON, and the caller's own copy is rewritten to agree. No call-level form
overrides it: Select("*"), Select(the column) and Omit(the column) were all
measured storing the default. Four of the five models
that carry such a tag were reachable, one of them on the client path, where it
meant a client an operator staged as disabled could connect.

The model set is derived from the tags rather than listed here on purpose. A
hardcoded list certifies only what someone remembered to enumerate, and the next
default:true column arrives on a model nobody listed — the same blind spot the
i18n dead-keys tokeniser has (ledger L7).

What this cannot check is the call sites: proving that a given Create is safe
needs the argument's type, and these tests parse rather than type-check. So it
requires the next-best thing that is checkable — a named regression test per
model, driven through the path an operator actually uses.
*/
func TestEveryTrueDefaultedBooleanHasACreateProof(t *testing.T) {
	root := repoRoot(t)
	models := modelsWithDefaultedBool(t, root)
	if len(models) == 0 {
		t.Fatal("no model carries a true-defaulted boolean; this guard is now vacuous — delete it or fix the scan")
	}

	proofs := readProofFile(t, root)
	for _, m := range models {
		if !strings.Contains(proofs, m.model) {
			t.Errorf(`%s.%s has a database-side default and no create proof.

A row created with this field false is stored TRUE and the struct is rewritten
to match. Add a case to internal/web/service/default_boolean_create_test.go that
creates one through the operator's own path with the field off and asserts it
reads back off — or, if nothing can create this row with an intended false, say
so there in a comment naming why.`, m.model, m.field)
		}
	}
}

type defaultedBool struct{ model, field string }

// modelsWithDefaultedBool reads the tags rather than a list, so a model added
// tomorrow is covered without anyone remembering this file exists.
func modelsWithDefaultedBool(t *testing.T, root string) []defaultedBool {
	t.Helper()
	path := filepath.Join(root, "internal", "database", "model", "model.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse model.go: %v", err)
	}

	var out []defaultedBool
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
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range st.Fields.List {
				ident, ok := field.Type.(*ast.Ident)
				if !ok || ident.Name != "bool" || field.Tag == nil {
					continue
				}
				raw, err := strconv.Unquote(field.Tag.Value)
				if err != nil {
					continue
				}
				// default:true only. A false default agrees with Go's zero value,
				// so omitting the field writes what the caller meant either way.
				gormTag := reflect.StructTag(raw).Get("gorm")
				if !strings.Contains(gormTag, "default:true") {
					continue
				}
				for _, name := range field.Names {
					out = append(out, defaultedBool{model: ts.Name.Name, field: name.Name})
				}
			}
		}
	}
	return out
}

func readProofFile(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "internal", "web", "service", "default_boolean_create_test.go")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the create-proof file is missing (%v); every defaulted boolean depends on it", err)
	}
	return string(body)
}
