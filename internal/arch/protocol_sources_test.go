package arch

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/cores"
)

/*
Which protocols exist is answered in three places:

  1. the core registry                (internal/cores) — the authority
  2. the model.Protocol const block   (internal/database/model/model.go)
  3. PROTOCOL_VALUES                  (frontend/src/generated/zod.ts)

(1) is what request validation reads, so it cannot drift from what the panel can
serve. (2) exists so Go can name a protocol without a string literal, and is
also what codegen reads — this tool is `go run` on a possibly-non-Linux
workstation and a core is built from Linux-only pieces. (3) is generated from
(2), so this arm catches a stale checked-in file rather than a hand-edit.

There used to be a fourth — a hand-typed `oneof=` list on Inbound.Protocol —
and it is the one that broke: it accepted tun, which no core claimed, so the
panel stored those inbounds and then refused to apply them.
*/

// knownProtocolDivergence maps a protocol value to why it is not in all three
// sources. Empty is the goal state: new divergence is a real failure.
var knownProtocolDivergence = map[string]string{}

// inboundProtocolValidation returns the rules on Inbound.Protocol. A `oneof=`
// among them means the hand-typed allow-list grew back.
func inboundProtocolValidation(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "internal", "database", "model", "model.go")
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse model.go: %v", err)
	}
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
				return reflect.StructTag(strings.Trim(field.Tag.Value, "`")).Get("validate")
			}
		}
	}
	t.Fatal("no Inbound.Protocol field found in model.go; this guard is vacuous")
	return ""
}

/*
TestInboundProtocolIsValidatedByTheRegistry is the guard against the fourth
source coming back.

A `oneof=` here would be accepted by the validator and read by openapigen, so it
would quietly become the allow-list again — and nothing else in the suite would
notice until a protocol was in one list and not the other.
*/
func TestInboundProtocolIsValidatedByTheRegistry(t *testing.T) {
	tag := inboundProtocolValidation(t, repoRoot(t))
	rules := strings.Split(tag, ",")
	if !slices.Contains(rules, "protocol") {
		t.Errorf("Inbound.Protocol has validate:%q, which does not include the `protocol` rule — request validation must ask the core registry, not a list", tag)
	}
	for _, rule := range rules {
		if strings.HasPrefix(rule, "oneof=") {
			t.Errorf("Inbound.Protocol has validate:%q — the hand-typed allow-list is back; register the core instead and let the `protocol` rule answer", tag)
		}
	}
}

// frontendProtocolValues reads the list the frontend actually ships. It is
// generated now, so this catches a stale checked-in file that `make gen` would
// have fixed — not a hand-edit, which is no longer possible.
func frontendProtocolValues(t *testing.T, root string) []string {
	t.Helper()
	path := filepath.Join(root, "frontend", "src", "generated", "zod.ts")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read generated zod.ts: %v", err)
	}
	enum := regexp.MustCompile(`(?s)PROTOCOL_VALUES = \[(.*?)\] as const`)
	match := enum.FindStringSubmatch(string(source))
	if match == nil {
		t.Fatal("no PROTOCOL_VALUES found in frontend/src/generated/zod.ts; openapigen stopped emitting it and this guard is vacuous")
	}
	values := regexp.MustCompile(`'([a-z0-9]+)'`).FindAllStringSubmatch(match[1], -1)
	if len(values) == 0 {
		t.Fatal("PROTOCOL_VALUES parsed to zero values; the parser no longer matches the file shape")
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
	registryValues := make([]string, 0, 16)
	for _, kind := range cores.Kinds() {
		registryValues = append(registryValues, string(kind))
	}
	if len(registryValues) == 0 {
		t.Fatal("the core registry claims no kinds; this guard is certifying nothing")
	}
	frontendValues := frontendProtocolValues(t, root)

	set := func(values []string) map[string]bool {
		out := make(map[string]bool, len(values))
		for _, v := range values {
			out[v] = true
		}
		return out
	}
	goSet, regSet, feSet := set(goValues), set(registryValues), set(frontendValues)

	union := map[string]bool{}
	for _, s := range []map[string]bool{goSet, regSet, feSet} {
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
		if goSet[value] && regSet[value] && feSet[value] {
			continue
		}
		diverged[value] = true
		if _, known := knownProtocolDivergence[value]; known {
			continue
		}
		t.Errorf("protocol %q is missing from at least one source (registry=%t, go const=%t, generated frontend list=%t) — the registry is the authority, the const block mirrors it, and the frontend list is generated from the const block (run `make gen`)",
			value, regSet[value], goSet[value], feSet[value])
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
