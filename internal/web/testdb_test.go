package web

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/Arman2122/p-ui/v3/internal/database"
)

var testSchemaSeq atomic.Int64

// initTestDB gives the calling test its own empty PostgreSQL schema and points
// the panel's database handle at it, so every test starts from a fresh install.
// PostgreSQL is the only supported backend, so a test that needs a database
// skips when PUI_DB_DSN names no server.
func initTestDB(t *testing.T) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("PUI_DB_DSN"))
	if dsn == "" {
		t.Skip("set PUI_DB_DSN to run the tests that need a live PostgreSQL server")
	}

	schema := fmt.Sprintf("pui_web_test_%d_%d", os.Getpid(), testSchemaSeq.Add(1))
	if err := execOnDSN(dsn, `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatalf("create test schema %s: %v", schema, err)
	}
	t.Cleanup(func() {
		_ = database.CloseDB()
		if err := execOnDSN(dsn, `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Logf("drop test schema %s: %v", schema, err)
		}
	})

	t.Setenv("PUI_DB_DSN", dsnWithSearchPath(dsn, schema))
	if err := database.InitDB(); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
}

// execOnDSN runs one statement on a short-lived connection, used for the schema
// bookkeeping that has to happen outside the panel's own connection.
func execOnDSN(dsn, statement string) error {
	gdb, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		return err
	}
	defer func() {
		if sqlDB, dbErr := gdb.DB(); dbErr == nil {
			_ = sqlDB.Close()
		}
	}()
	return gdb.Exec(statement).Error
}

// dsnWithSearchPath pins a DSN to one schema, in whichever of the two shapes
// pgx accepts the caller wrote it.
func dsnWithSearchPath(dsn, schema string) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		separator := "?"
		if strings.Contains(dsn, "?") {
			separator = "&"
		}
		return dsn + separator + "search_path=" + schema
	}
	return dsn + " search_path=" + schema
}
