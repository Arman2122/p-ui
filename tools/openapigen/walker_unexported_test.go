package main

import (
	"os"
	"path/filepath"
	"testing"
)

/*
An unexported Go type must never reach the generated frontend surface.

The walker emitted every named non-struct type of a package whose request
carried a nil AliasAllow, exported or not, and internal/web/service carried
none -- so `staticEgressResolver` and `transportBits`, lowercase helpers no
API response can contain, were shipped as frontend types. F409.
*/
func TestWalkerSkipsUnexportedTypesEvenWithNoAllowlist(t *testing.T) {
	dir := t.TempDir()
	src := "package fixture\n\n" +
		"type helper = string\n\n" +
		"type Visible string\n\n" +
		"type row struct{ A int }\n\n" +
		"type Row struct{ A int }\n"
	if err := os.WriteFile(filepath.Join(dir, "fixture.go"), []byte(src), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	schemas, aliases, err := walkPackages([]packageRequest{{Path: dir}})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(aliases) != 1 || aliases[0].Name != "Visible" {
		t.Fatalf("aliases = %+v, want only Visible: an unexported alias leaked", names(aliases))
	}
	if len(schemas) != 1 || schemas[0].Name != "Row" {
		t.Fatalf("schemas = %+v, want only Row: an unexported struct leaked", schemaNames(schemas))
	}
}

func names(aliases []Alias) []string {
	out := make([]string, 0, len(aliases))
	for _, a := range aliases {
		out = append(out, a.Name)
	}
	return out
}

func schemaNames(schemas []Schema) []string {
	out := make([]string, 0, len(schemas))
	for _, s := range schemas {
		out = append(out, s.Name)
	}
	return out
}
