package main

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
)

type Schema struct {
	Name    string
	Package string
	Fields  []Field
	Doc     string
}

type Alias struct {
	Name       string
	Package    string
	Underlying TypeRef
	// Values are the constants declared with this type, in declaration order.
	// A named string type with constants is a closed set, so it is emitted as a
	// union rather than as `string` — which is what let the frontend keep its
	// own copy of the protocol list.
	Values []string
}

type Field struct {
	JSONName string
	GoName   string
	Type     TypeRef
	Optional bool
	Skip     bool
	Validate []ValidateRule
	Doc      string
	Example  string
}

type TypeRef struct {
	Kind    TypeKind
	Name    string
	Element *TypeRef
	Key     *TypeRef
	Value   *TypeRef
	Inner   *TypeRef
}

type TypeKind string

const (
	KindString  TypeKind = "string"
	KindNumber  TypeKind = "number"
	KindInt     TypeKind = "int"
	KindBool    TypeKind = "boolean"
	KindArray   TypeKind = "array"
	KindMap     TypeKind = "map"
	KindObject  TypeKind = "object"
	KindRef     TypeKind = "ref"
	KindUnknown TypeKind = "unknown"
	KindAny     TypeKind = "any"
	KindRaw     TypeKind = "raw"
)

type ValidateRule struct {
	Name  string
	Param string
}

func parseStructTag(raw string) (json string, validate string, example string, gormHasDash bool) {
	tag := reflect.StructTag(strings.Trim(raw, "`"))
	json = tag.Get("json")
	validate = tag.Get("validate")
	example = tag.Get("example")
	if g := tag.Get("gorm"); g != "" {
		for part := range strings.SplitSeq(g, ";") {
			if strings.TrimSpace(part) == "-" {
				gormHasDash = true
			}
		}
	}
	return
}

func parseJSONTag(tag string) (name string, omit bool, omitempty bool) {
	if tag == "" {
		return "", false, false
	}
	parts := strings.Split(tag, ",")
	name = parts[0]
	if name == "-" {
		return "", true, false
	}
	for _, p := range parts[1:] {
		if p == "omitempty" {
			omitempty = true
		}
	}
	return
}

func parseValidateTag(tag string) []ValidateRule {
	if tag == "" {
		return nil
	}
	var rules []ValidateRule
	for part := range strings.SplitSeq(tag, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		before, after, ok := strings.Cut(part, "=")
		if !ok {
			rules = append(rules, ValidateRule{Name: part})
			continue
		}
		rules = append(rules, ValidateRule{Name: before, Param: after})
	}
	return rules
}

/*
expandProtocolRules turns the panel's `protocol` rule into the enum the emitters
already understand, so a registry-backed rule still generates a closed union.

values come from the model.Protocol constants rather than the registry itself:
this tool is `go run` on the developer's machine, and a core is built out of
Linux-only pieces. TestProtocolSourcesAgree pins the constants to the registry.
*/
func expandProtocolRules(schemas []Schema, values []string) error {
	expanded := ValidateRule{Name: "oneof", Param: strings.Join(values, " ")}
	for si := range schemas {
		for fi := range schemas[si].Fields {
			for ri, rule := range schemas[si].Fields[fi].Validate {
				if rule.Name != "protocol" {
					continue
				}
				if len(values) == 0 {
					return fmt.Errorf("%s.%s uses the protocol rule but no model.Protocol constants were found; the enum would generate empty", schemas[si].Name, schemas[si].Fields[fi].JSONName)
				}
				schemas[si].Fields[fi].Validate[ri] = expanded
			}
		}
	}
	return nil
}

func (s Schema) HasValidationOn(field string) bool {
	for _, f := range s.Fields {
		if f.JSONName == field {
			return len(f.Validate) > 0
		}
	}
	return false
}

func sortSchemas(in []Schema) []Schema {
	out := make([]Schema, len(in))
	copy(out, in)
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func sortAliases(in []Alias) []Alias {
	out := make([]Alias, len(in))
	copy(out, in)
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

func flattenEmbedded(schemas []Schema) []Schema {
	byName := make(map[string]Schema, len(schemas))
	for _, s := range schemas {
		byName[s.Name] = s
	}
	out := make([]Schema, 0, len(schemas))
	for _, s := range schemas {
		var resolved []Field
		seen := make(map[string]bool, len(s.Fields))
		for _, f := range s.Fields {
			if f.Type.Kind == KindRef && f.Type.Name != "nullable" {
				if embedded, ok := byName[f.Type.Name]; ok && f.GoName == f.Type.Name {
					for _, ef := range embedded.Fields {
						if seen[ef.JSONName] {
							continue
						}
						seen[ef.JSONName] = true
						resolved = append(resolved, ef)
					}
					continue
				}
			}
			if seen[f.JSONName] {
				continue
			}
			seen[f.JSONName] = true
			resolved = append(resolved, f)
		}
		s.Fields = resolved
		out = append(out, s)
	}
	return out
}

// aliasTypeExpr renders a named type. A string type with constants becomes the
// union of them, which is what makes the frontend's copy of the list deletable.
func aliasTypeExpr(a Alias) string {
	if len(a.Values) == 0 {
		return tsTypeExpr(a.Underlying)
	}
	quoted := make([]string, 0, len(a.Values))
	for _, v := range a.Values {
		quoted = append(quoted, fmt.Sprintf("'%s'", v))
	}
	return strings.Join(quoted, " | ")
}

// aliasValuesConst renders the constants as a runtime array. A TypeScript union
// cannot be iterated, and Zod needs the values to build an enum from.
func aliasValuesConst(a Alias) string {
	quoted := make([]string, 0, len(a.Values))
	for _, v := range a.Values {
		quoted = append(quoted, fmt.Sprintf("'%s'", v))
	}
	return fmt.Sprintf("export const %s_VALUES = [%s] as const;\n", strings.ToUpper(a.Name), strings.Join(quoted, ", "))
}

// aliasValues returns the constants collected for a named type, or nil when the
// tool never saw it — which expandProtocolRules turns into a loud failure.
func aliasValues(aliases []Alias, name string) []string {
	for _, a := range aliases {
		if a.Name == name {
			return a.Values
		}
	}
	return nil
}
