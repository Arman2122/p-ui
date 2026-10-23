package main

import (
	"encoding/json"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
)

func TestGeneratedNamedAndEmbeddedFieldsMatchJSON(t *testing.T) {
	const source = "package fixture\ntype Client struct { Email string `json:\"email\"` }\ntype Named struct { Client Client `json:\"client\"` }\ntype Embedded struct { Client }\ntype Tagged struct { Client `json:\"nested\"` }\n"
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "fixture.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	schemas, _, err := walkPackages([]packageRequest{{Path: directory}})
	if err != nil {
		t.Fatal(err)
	}
	type Client struct {
		Email string `json:"email"`
	}
	wires := map[string]any{
		"Client": Client{Email: "client"},
		"Named": struct {
			Client Client `json:"client"`
		}{Client{Email: "client"}},
		"Embedded": struct{ Client }{Client{Email: "client"}},
		"Tagged": struct {
			Client `json:"nested"`
		}{Client{Email: "client"}},
	}
	for _, schema := range flattenEmbedded(schemas) {
		payload, err := json.Marshal(wires[schema.Name])
		if err != nil {
			t.Fatal(err)
		}
		var wire map[string]json.RawMessage
		if err := json.Unmarshal(payload, &wire); err != nil {
			t.Fatal(err)
		}
		if len(wire) != len(schema.Fields) {
			t.Fatalf("%s generated field count differs from JSON", schema.Name)
		}
		for _, field := range schema.Fields {
			if _, exists := wire[field.JSONName]; !exists {
				t.Errorf("%s.%s is flattened differently from actual JSON %s", schema.Name, field.JSONName, payload)
			}
		}
	}
}

func TestGeneratedNetworkAndAssetShapesMatchJSON(t *testing.T) {
	const source = "package fixture\nimport \"net/netip\"\ntype Wire struct { Data []byte `json:\"data\"`; Prefix netip.Prefix `json:\"prefix\"`; Address netip.Addr `json:\"address\"`; Endpoint netip.AddrPort `json:\"endpoint\"`; Fixed [2]byte `json:\"fixed\"`; Inbounds map[int]string `json:\"inbounds\"` }\n"
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "fixture.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	schemas, _, err := walkPackages([]packageRequest{{Path: directory, StructAllow: setOf("Wire")}})
	if err != nil || len(schemas) != 1 {
		t.Fatalf("walk schemas=%d err=%v", len(schemas), err)
	}
	payload, err := json.Marshal(struct {
		Data     []byte         `json:"data"`
		Prefix   netip.Prefix   `json:"prefix"`
		Address  netip.Addr     `json:"address"`
		Endpoint netip.AddrPort `json:"endpoint"`
		Fixed    [2]byte        `json:"fixed"`
		Inbounds map[int]string `json:"inbounds"`
	}{[]byte{1, 2, 3}, netip.MustParsePrefix("10.0.0.0/24"), netip.MustParseAddr("2001:db8::1"), netip.MustParseAddrPort("127.0.0.1:7443"), [2]byte{1, 2}, map[int]string{7: "listener"}})
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]any
	if err := json.Unmarshal(payload, &wire); err != nil {
		t.Fatal(err)
	}
	if wire["data"] != "AQID" {
		t.Fatalf("unexpected Go binary wire encoding: %v", wire["data"])
	}
	gen := &schemaGen{}
	for _, field := range schemas[0].Fields {
		expected := "string"
		if _, array := wire[field.JSONName].([]any); array {
			expected = "array"
		} else if _, object := wire[field.JSONName].(map[string]any); object {
			expected = "object"
			if field.Type.Kind != KindMap || field.Type.Key == nil || zodTypeExpr(*field.Type.Key) != "z.string()" {
				t.Errorf("%s JSON keys are strings, generated key schema=%v", field.JSONName, field.Type.Key)
			}
		} else if _, text := wire[field.JSONName].(string); !text {
			t.Fatalf("unexpected wire type %T", wire[field.JSONName])
		}
		actual := gen.typeSchema(field.Type)
		if actual["type"] != expected {
			t.Errorf("%s JSON is %s, generated schema=%v", field.JSONName, expected, actual)
		}
	}
}
