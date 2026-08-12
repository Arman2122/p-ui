package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPolicyMarshalJSONEmitsTheLadderAsAnArray(t *testing.T) {
	row := Policy{Id: 1, Name: "fair use", Tiers: `[{"fromBytes":0,"upBps":0,"downBps":0}]`}
	out, err := json.Marshal(row)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	if strings.Contains(string(out), `"tiers":"`) {
		t.Errorf("the ladder must not cross the API as JSON text: %s", out)
	}
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	tiers, ok := parsed["tiers"].([]any)
	if !ok {
		t.Fatalf("expected tiers to marshal as an array, got %T", parsed["tiers"])
	}
	if len(tiers) != 1 {
		t.Fatalf("expected one tier, got %d", len(tiers))
	}
}

func TestPolicyMarshalJSONEmptyLadderIsNull(t *testing.T) {
	out, err := json.Marshal(Policy{Id: 1, Name: "empty"})
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v", err)
	}
	if parsed["tiers"] != nil {
		t.Errorf("expected tiers to be null, got %v", parsed["tiers"])
	}
}

// The array body is the one every published example and the OpenAPI schema
// carry, so a bind that rejects it makes the documentation unusable.
func TestPolicyUnmarshalJSONAcceptsBothShapes(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{
			name: "an array, the documented shape",
			body: `{"name":"fair use","tiers":[{"fromBytes":0,"upBps":0,"downBps":0},{"fromBytes":53687091200,"upBps":10000000,"downBps":10000000}]}`,
		},
		{
			name: "JSON text, the shape the column stores",
			body: `{"name":"fair use","tiers":"[{\"fromBytes\":0,\"upBps\":0,\"downBps\":0},{\"fromBytes\":53687091200,\"upBps\":10000000,\"downBps\":10000000}]"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var row Policy
			if err := json.Unmarshal([]byte(tc.body), &row); err != nil {
				t.Fatalf("the panel rejected its own documented body: %v", err)
			}
			if row.Name != "fair use" {
				t.Errorf("name = %q, want %q", row.Name, "fair use")
			}
			var tiers []struct {
				FromBytes int64 `json:"fromBytes"`
				UpBps     int64 `json:"upBps"`
				DownBps   int64 `json:"downBps"`
			}
			if err := json.Unmarshal([]byte(row.Tiers), &tiers); err != nil {
				t.Fatalf("the stored column is not the JSON text the service parses: %v", err)
			}
			if len(tiers) != 2 {
				t.Fatalf("expected two tiers, got %d", len(tiers))
			}
			if tiers[1].FromBytes != 53687091200 || tiers[1].DownBps != 10000000 {
				t.Errorf("second tier = %+v, want the 50 GB / 10 Mbps row", tiers[1])
			}
		})
	}
}

func TestPolicyUnmarshalJSONAbsentLadderStaysEmpty(t *testing.T) {
	row := Policy{Tiers: `[{"fromBytes":0}]`}
	if err := json.Unmarshal([]byte(`{"name":"renamed"}`), &row); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if row.Tiers != "" {
		t.Errorf("tiers = %q, want empty: a body without a ladder must not keep a stale one", row.Tiers)
	}
}
