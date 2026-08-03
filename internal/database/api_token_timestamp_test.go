package database

import (
	"testing"

	"github.com/Arman2122/p-ui/v3/internal/database/model"
)

func TestNormalizeApiTokenCreatedAtSeconds(t *testing.T) {
	initTestDB(t)

	rows := []model.ApiToken{
		{Name: "seconds", Token: "a", CreatedAt: 1_782_485_394},
		{Name: "milliseconds", Token: "b", CreatedAt: 1_782_485_394_270},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed api tokens: %v", err)
	}

	if err := normalizeApiTokenCreatedAtSeconds(); err != nil {
		t.Fatalf("normalize timestamps: %v", err)
	}
	if err := normalizeApiTokenCreatedAtSeconds(); err != nil {
		t.Fatalf("normalize timestamps again: %v", err)
	}

	var got []model.ApiToken
	if err := db.Where("name IN ?", []string{"seconds", "milliseconds"}).Order("id asc").Find(&got).Error; err != nil {
		t.Fatalf("read api tokens: %v", err)
	}
	if len(got) != len(rows) {
		t.Fatalf("read %d api tokens, want %d", len(got), len(rows))
	}
	for _, row := range got {
		if row.CreatedAt != 1_782_485_394 {
			t.Fatalf("%s created_at = %d, want seconds", row.Name, row.CreatedAt)
		}
	}
}
