package model

import (
	"encoding/json"
	"testing"
)

func TestPlanPartialJSONKeepsOmittedFieldsAndClearsExplicitNulls(t *testing.T) {
	quota := int64(1000)
	days := 30
	ladder := `[{"fromBytes":0,"upBps":42,"downBps":84}]`
	row := Policy{Name: "before", Tiers: ladder, QuotaBytes: &quota, DurationDays: &days}
	if err := json.Unmarshal([]byte(`{"description":"updated","quotaBytes":null}`), &row); err != nil {
		t.Fatal(err)
	}
	if row.Tiers != ladder || row.Name != "before" || row.DurationDays == nil || *row.DurationDays != 30 || row.QuotaBytes != nil {
		t.Fatalf("partial update changed omitted values: %+v", row)
	}
	if err := json.Unmarshal([]byte(`{"tiers":null}`), &row); err != nil {
		t.Fatal(err)
	}
	if row.Tiers != "" {
		t.Fatalf("explicit null kept the ladder: %q", row.Tiers)
	}
	if err := json.Unmarshal([]byte(`{"tiers":[],"quotaBytes":0}`), &row); err != nil {
		t.Fatal(err)
	}
	if row.Tiers != "[]" || row.QuotaBytes == nil || *row.QuotaBytes != 0 {
		t.Fatalf("explicit unlimited did not persist: %+v", row)
	}
}
