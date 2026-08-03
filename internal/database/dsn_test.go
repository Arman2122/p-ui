package database

import (
	"strings"
	"testing"
)

// TestParseDSNAccepts pins the one DSN shape Penhoon UI supports: a postgres://
// URL that names a database. Startup and the pg_dump/pg_restore backup paths
// share this parser, so whatever is accepted here must also survive being split
// into PG* variables.
func TestParseDSNAccepts(t *testing.T) {
	cases := []struct {
		name   string
		dsn    string
		dbname string
		host   string
		port   string
	}{
		{"full url", "postgres://p-ui:PASSWORD@127.0.0.1:5432/p-ui?sslmode=disable", "p-ui", "127.0.0.1", "5432"},
		{"postgresql scheme", "postgresql://pui@db.internal/pui", "pui", "db.internal", ""},
		{"search_path appended by the test helpers", "postgres://pui:pui@127.0.0.1:5432/pui?sslmode=disable&search_path=pui_test_1", "pui", "127.0.0.1", "5432"},
		{"surrounding whitespace", "  postgres://pui:pui@127.0.0.1:5432/pui  ", "pui", "127.0.0.1", "5432"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			u, err := ParseDSN(tc.dsn)
			if err != nil {
				t.Fatalf("ParseDSN(%q) = %v, want no error", tc.dsn, err)
			}
			if got := strings.TrimPrefix(u.Path, "/"); got != tc.dbname {
				t.Errorf("ParseDSN(%q) database = %q, want %q", tc.dsn, got, tc.dbname)
			}
			if got := u.Hostname(); got != tc.host {
				t.Errorf("ParseDSN(%q) host = %q, want %q", tc.dsn, got, tc.host)
			}
			if got := u.Port(); got != tc.port {
				t.Errorf("ParseDSN(%q) port = %q, want %q", tc.dsn, got, tc.port)
			}
		})
	}
}

// TestParseDSNRejects covers everything the panel must refuse at startup rather
// than boot on and break later. The libpq keyword form is the important one:
// net/url cannot split it, so a panel that accepted it would run fine until the
// first Back Up, Telegram backup or Import DB.
func TestParseDSNRejects(t *testing.T) {
	cases := []struct {
		name string
		dsn  string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"libpq keyword form", "host=127.0.0.1 port=5432 user=pui password=pui dbname=pui sslmode=disable"},
		{"leftover sqlite path", "/etc/p-ui/p-ui.db"},
		{"wrong scheme", "mysql://pui:pui@127.0.0.1:3306/pui"},
		{"no database name", "postgres://pui:pui@127.0.0.1:5432"},
		{"empty database name", "postgres://pui:pui@127.0.0.1:5432/"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseDSN(tc.dsn); err == nil {
				t.Fatalf("ParseDSN(%q) = nil error, want a rejection", tc.dsn)
			}
		})
	}
}

// TestExampleDSNIsValid keeps the copy-pasteable DSN in the startup errors from
// drifting into something the panel would itself reject.
func TestExampleDSNIsValid(t *testing.T) {
	if _, err := ParseDSN(exampleDSN); err != nil {
		t.Fatalf("exampleDSN %q is not accepted by ParseDSN: %v", exampleDSN, err)
	}
}

func TestRequireDSN(t *testing.T) {
	t.Run("missing", func(t *testing.T) {
		t.Setenv("PUI_DB_DSN", "")
		if _, err := requireDSN(); err == nil {
			t.Fatal("requireDSN() = nil error with PUI_DB_DSN unset, want a rejection")
		}
	})
	t.Run("libpq keyword form", func(t *testing.T) {
		t.Setenv("PUI_DB_DSN", "host=127.0.0.1 port=5432 user=pui dbname=pui sslmode=disable")
		_, err := requireDSN()
		if err == nil {
			t.Fatal("requireDSN() accepted the libpq keyword form, want a rejection")
		}
		// The message has to point the operator at the format that works.
		if !strings.Contains(err.Error(), "postgres://") {
			t.Errorf("requireDSN() error %q does not show a postgres:// example", err)
		}
	})
	t.Run("url", func(t *testing.T) {
		const dsn = "postgres://pui:pui@127.0.0.1:5432/pui?sslmode=disable"
		t.Setenv("PUI_DB_DSN", dsn)
		got, err := requireDSN()
		if err != nil {
			t.Fatalf("requireDSN() = %v, want no error", err)
		}
		if got != dsn {
			t.Errorf("requireDSN() = %q, want %q", got, dsn)
		}
	})
}
