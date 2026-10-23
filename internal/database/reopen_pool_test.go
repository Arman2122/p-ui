package database

import (
	"strings"
	"testing"
)

func TestInitDBClosesOnlyAReplacedPool(t *testing.T) {
	initTestDB(t)
	old, err := GetDB().DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := InitDB(); err != nil {
		t.Fatal(err)
	}
	if err := old.Ping(); err == nil || err.Error() != "sql: database is closed" {
		t.Fatalf("replaced pool ping = %v, want closed pool", err)
	}
	current, err := GetDB().DB()
	if err != nil || current == old {
		t.Fatalf("replacement pool = %p, err = %v", current, err)
	}
	t.Setenv("PUI_DB_DSN", "")
	if err := InitDB(); err == nil || !strings.Contains(err.Error(), "PUI_DB_DSN is not set") {
		t.Fatalf("bad reopen = %v, want missing DSN error", err)
	}
	if err := current.Ping(); err != nil {
		t.Fatalf("failed reopen closed the working pool: %v", err)
	}
}
