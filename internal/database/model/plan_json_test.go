package model

import (
	"encoding/json"
	"testing"
)

func TestPlanPartialJSONPreservesDurationAndClearsManagedQuota(t *testing.T) {
	quota := int64(1000)
	days := 30
	row := Policy{Name: "before", Tiers: `[{"fromBytes":0,"upBps":42,"downBps":84}]`, QuotaBytes: &quota, DurationDays: &days}
	if err := json.Unmarshal([]byte(`{"description":"updated","quotaBytes":null}`), &row); err != nil {
		t.Fatal(err)
	}
	if row.Tiers != "" || row.Name != "before" || row.DurationDays == nil || *row.DurationDays != 30 || row.QuotaBytes != nil {
		t.Fatalf("partial update changed omitted values: %+v", row)
	}
	if err := json.Unmarshal([]byte(`{"tiers":[],"quotaBytes":0}`), &row); err != nil {
		t.Fatal(err)
	}
	if row.Tiers != "[]" || row.QuotaBytes == nil || *row.QuotaBytes != 0 {
		t.Fatalf("explicit unlimited did not persist: %+v", row)
	}
}
