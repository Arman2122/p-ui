package arch

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

/*
Which protocols exist is answered in three places that nothing cross-checks:

  1. the model.Protocol const block          (internal/database/model/model.go)
  2. the Inbound.Protocol `oneof=` validator tag on the same struct
  3. the frontend ProtocolSchema enum        (frontend/src/schemas/primitives/protocol.ts)

They already disagree, and the comment in (3) explaining the disagreement is
itself wrong — it says the Go validator "no longer accepts" tun, which it does.
That is what an unenforced convention decays into, and it is the reason the
refactor replaces all three with one registry.

This test pins the divergence rather than resolving it: whether p-ui supports
Xray tun inbounds is a product decision, not a mechanical one. New divergence
fails; resolving the pinned one fails too, so the pin cannot outlive the fix.
*/

// knownProtocolDivergence maps a protocol value to why it is not in all three
// sources yet. Empty this as the registry replaces the hand-maintained lists.
//
// Empty is the goal state, not a reason to relax: the three sources now agree,
// so any new divergence is a real failure rather than an inherited one.
var knownProtocolDivergence = map[string]string{}

func inboundProtocolTagValues(t *testing.T, root string) []string {
	t.Helper()
	path := filepath.Join(root, "internal", "database", "model", "model.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse model.go: %v", err)
	}
	oneof := regexp.MustCompile(`oneof=([a-z0-9 ]+)`)
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != "Inbound" {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range st.Fields.List {
				if len(field.Names) == 0 || field.Names[0].Name != "Protocol" || field.Tag == nil {
					continue
				}
				match := oneof.FindStringSubmatch(field.Tag.Value)
				if match == nil {
					t.Fatal("Inbound.Protocol has no oneof= in its validate tag; the allow-list moved and this guard is vacuous")
				}
				return strings.Fields(match[1])
			}
		}
	}
	t.Fatal("no Inbound.Protocol field found in model.go; this guard is vacuous")
	return nil
}

func frontendProtocolValues(t *testing.T, root string) []string {
	t.Helper()
	path := filepath.Join(root, "frontend", "src", "schemas", "primitives", "protocol.ts")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read protocol.ts: %v", err)
	}
	enum := regexp.MustCompile(`(?s)ProtocolSchema = z\.enum\(\[(.*?)\]\)`)
	match := enum.FindStringSubmatch(string(source))
	if match == nil {
		t.Fatal("no ProtocolSchema z.enum found in protocol.ts; the frontend enum moved and this guard is vacuous")
	}
	values := regexp.MustCompile(`'([a-z0-9]+)'`).FindAllStringSubmatch(match[1], -1)
	if len(values) == 0 {
		t.Fatal("ProtocolSchema parsed to zero values; the parser no longer matches the file shape")
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		out = append(out, v[1])
	}
	return out
}

func TestProtocolSourcesAgree(t *testing.T) {
	root := repoRoot(t)

	goValues := make([]string, 0, 16)
	for _, value := range protocolConstants(t, root) {
		goValues = append(goValues, value)
	}
	tagValues := inboundProtocolTagValues(t, root)
	frontendValues := frontendProtocolValues(t, root)

	set := func(values []string) map[string]bool {
		out := make(map[string]bool, len(values))
		for _, v := range values {
			out[v] = true
		}
		return out
	}
	goSet, tagSet, feSet := set(goValues), set(tagValues), set(frontendValues)

	union := map[string]bool{}
	for _, s := range []map[string]bool{goSet, tagSet, feSet} {
		for v := range s {
			union[v] = true
		}
	}
	all := make([]string, 0, len(union))
	for v := range union {
		all = append(all, v)
	}
	sort.Strings(all)

	diverged := map[string]bool{}
	for _, value := range all {
		if goSet[value] && tagSet[value] && feSet[value] {
			continue
		}
		diverged[value] = true
		if _, known := knownProtocolDivergence[value]; known {
			continue
		}
		t.Errorf("protocol %q is missing from at least one source (go const=%t, validator tag=%t, frontend enum=%t) — all three are hand-maintained and must agree until the core registry replaces them",
			value, goSet[value], tagSet[value], feSet[value])
	}

	for value, why := range knownProtocolDivergence {
		if !diverged[value] {
			t.Errorf("protocol %q no longer diverges — remove it from knownProtocolDivergence (pinned because: %s)", value, why)
		}
	}
}

// TestProtocolConstantNamesAreUnique catches a copy-paste that silently gives two
// constants the same wire value, which would make registry lookup ambiguous later.
func TestProtocolConstantNamesAreUnique(t *testing.T) {
	root := repoRoot(t)
	byValue := map[string][]string{}
	for name, value := range protocolConstants(t, root) {
		byValue[value] = append(byValue[value], name)
	}
	for value, names := range byValue {
		if len(names) > 1 {
			sort.Strings(names)
			t.Errorf("protocol value %q is declared by %v — one wire value must map to exactly one constant", value, names)
		}
	}
}
