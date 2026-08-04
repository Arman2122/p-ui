package main

import "testing"

/*
An empty expansion is the failure that would ship quietly: z.enum([]) and
"enum": [] are both valid output, so the frontend would reject every protocol
while the panel accepted them, and gen-check would see no drift.

It is reachable — namedStringConstants finds nothing if the model.Protocol const
block is renamed or moved — so the tool has to refuse rather than emit it.
*/
func TestExpandProtocolRulesRefusesAnEmptyEnum(t *testing.T) {
	withProtocolField := func() []Schema {
		return []Schema{{
			Name:   "Inbound",
			Fields: []Field{{JSONName: "protocol", Validate: []ValidateRule{{Name: "required"}, {Name: "protocol"}}}},
		}}
	}

	if err := expandProtocolRules(withProtocolField(), nil); err == nil {
		t.Error("expanding with no constants succeeded; the generated enum would be empty and reject every protocol")
	}

	schemas := withProtocolField()
	if err := expandProtocolRules(schemas, []string{"vmess", "vless"}); err != nil {
		t.Fatalf("expand: %v", err)
	}
	got := schemas[0].Fields[0].Validate
	if len(got) != 2 || got[1].Name != "oneof" || got[1].Param != "vmess vless" {
		t.Errorf("rules = %+v, want the protocol rule replaced by oneof=vmess vless", got)
	}

	// No protocol rule means no constants are needed; every other struct in the
	// repo is in this case and must not start failing the generator.
	noProtocol := []Schema{{Name: "Host", Fields: []Field{{JSONName: "port", Validate: []ValidateRule{{Name: "required"}}}}}}
	if err := expandProtocolRules(noProtocol, nil); err != nil {
		t.Errorf("expanding a schema without a protocol rule failed: %v", err)
	}
}
