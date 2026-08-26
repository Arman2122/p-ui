package service

import (
	"strings"
	"testing"
)

// lastEnvValue reads a variable out of a pgConnEnv result. pgConnEnv appends its
// PG* entries after os.Environ(), so the last match is the one the child process
// sees even when the panel's own environment already carries that variable.
func lastEnvValue(env []string, key string) (string, bool) {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], prefix) {
			return strings.TrimPrefix(env[i], prefix), true
		}
	}
	return "", false
}

// TestPgConnEnvSplitsURLDSN checks the pg_dump/pg_restore environment is built
// from the DSN URL, and that the password never leaves the environment.
func TestPgConnEnvSplitsURLDSN(t *testing.T) {
	const dsn = "postgres://p-ui:s3cr3t@db.internal:6543/panel?sslmode=require"
	env, dbname, err := pgConnEnv(dsn)
	if err != nil {
		t.Fatalf("pgConnEnv(%q) = %v, want no error", dsn, err)
	}
	if dbname != "panel" {
		t.Errorf("pgConnEnv dbname = %q, want %q", dbname, "panel")
	}
	want := map[string]string{
		"PGHOST":     "db.internal",
		"PGPORT":     "6543",
		"PGDATABASE": "panel",
		"PGUSER":     "p-ui",
		"PGPASSWORD": "s3cr3t",
		"PGSSLMODE":  "require",
	}
	for key, wantValue := range want {
		got, ok := lastEnvValue(env, key)
		if !ok {
			t.Errorf("pgConnEnv did not set %s", key)
			continue
		}
		if got != wantValue {
			t.Errorf("pgConnEnv %s = %q, want %q", key, got, wantValue)
		}
	}
}

// TestPgConnEnvDefaultsHostAndPort covers a DSN that leaves the server implicit.
func TestPgConnEnvDefaultsHostAndPort(t *testing.T) {
	env, dbname, err := pgConnEnv("postgres:///panel")
	if err != nil {
		t.Fatalf("pgConnEnv = %v, want no error", err)
	}
	if dbname != "panel" {
		t.Errorf("pgConnEnv dbname = %q, want %q", dbname, "panel")
	}
	if got, _ := lastEnvValue(env, "PGHOST"); got != "127.0.0.1" {
		t.Errorf("pgConnEnv PGHOST = %q, want %q", got, "127.0.0.1")
	}
	if got, _ := lastEnvValue(env, "PGPORT"); got != "5432" {
		t.Errorf("pgConnEnv PGPORT = %q, want %q", got, "5432")
	}
}

// TestPgConnEnvRejectsNonURLDSN is the regression guard for the split contract
// this used to have: the panel booted on a libpq keyword DSN that Back Up,
// the Telegram scheduled backup and Import DB could not parse. Both sides now
// go through database.ParseDSN, so a DSN the panel refuses to start on is the
// only DSN this refuses too.
func TestPgConnEnvRejectsNonURLDSN(t *testing.T) {
	cases := []string{
		"",
		"host=127.0.0.1 port=5432 user=pui password=pui dbname=pui sslmode=disable",
		"/etc/p-ui/p-ui.db",
		"mysql://pui:pui@127.0.0.1:3306/pui",
		"postgres://pui:pui@127.0.0.1:5432",
	}
	for _, dsn := range cases {
		if _, _, err := pgConnEnv(dsn); err == nil {
			t.Errorf("pgConnEnv(%q) = nil error, want a rejection", dsn)
		}
	}
}

/*
Every libpq parameter the DSN carries reaches pg_dump, not just sslmode.

pgx honours the whole URL when the panel connects, so a managed-Postgres DSN
naming a CA bundle boots and runs. pgConnEnv forwarded only sslmode, so pg_dump
got PGSSLMODE=verify-full with no PGSSLROOTCERT, fell back to a
~/.postgresql/root.crt the service user does not have, and failed — Back Up, the
scheduled Telegram backup and Import DB all broken on a healthy panel. F272.
*/
func TestPgConnEnvForwardsEveryLibpqParameter(t *testing.T) {
	dsn := "postgres://pui:secret@db.example.net:25060/pui" +
		"?sslmode=verify-full" +
		"&sslrootcert=/etc/p-ui/ca.crt" +
		"&sslcert=/etc/p-ui/client.crt" +
		"&sslkey=/etc/p-ui/client.key" +
		"&connect_timeout=10" +
		"&application_name=p-ui" +
		"&target_session_attrs=read-write"

	env, dbname, err := pgConnEnv(dsn)
	if err != nil {
		t.Fatalf("pgConnEnv: %v", err)
	}
	if dbname != "pui" {
		t.Fatalf("dbname = %q, want %q", dbname, "pui")
	}

	want := map[string]string{
		"PGSSLMODE":            "verify-full",
		"PGSSLROOTCERT":        "/etc/p-ui/ca.crt",
		"PGSSLCERT":            "/etc/p-ui/client.crt",
		"PGSSLKEY":             "/etc/p-ui/client.key",
		"PGCONNECT_TIMEOUT":    "10",
		"PGAPPNAME":            "p-ui",
		"PGTARGETSESSIONATTRS": "read-write",
	}
	for key, value := range want {
		got, ok := lastEnvValue(env, key)
		if !ok || got != value {
			t.Errorf("%s = %q (present=%v), want %q — a parameter the panel connects with was dropped before pg_dump", key, got, ok, value)
		}
	}

	// The password reaches the child only through the environment, never as an
	// argument where it would land in the process table.
	if got, ok := lastEnvValue(env, "PGPASSWORD"); !ok || got != "secret" {
		t.Fatalf("PGPASSWORD = %q (present=%v), want the DSN's password", got, ok)
	}
}

// A parameter libpq does not know stays dropped rather than being forwarded into
// a hard error. search_path is pgx-only and this repo's own test helpers append
// it, so forwarding the DSN verbatim would break Back Up on every test database.
func TestPgConnEnvDropsParametersLibpqWouldReject(t *testing.T) {
	env, _, err := pgConnEnv("postgres://pui@localhost/pui?sslmode=disable&search_path=pui_test_1")
	if err != nil {
		t.Fatalf("pgConnEnv must accept a pgx-only parameter, got: %v", err)
	}
	for _, entry := range env {
		if strings.Contains(entry, "pui_test_1") {
			t.Fatalf("a pgx-only parameter was forwarded to libpq: %q", entry)
		}
	}
}
