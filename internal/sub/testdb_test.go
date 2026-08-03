package sub

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/Arman2122/p-ui/v3/internal/database"
)

var subTestSchemaSeq atomic.Int64

// initSubDB gives the calling test its own empty PostgreSQL schema and points
// the panel's database handle at it, so every test starts from a fresh install.
// PostgreSQL is the only supported backend, so a test that needs a database
// skips when PUI_DB_DSN names no server.
func initSubDB(t *testing.T) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("PUI_DB_DSN"))
	if dsn == "" {
		t.Skip("set PUI_DB_DSN to run the tests that need a live PostgreSQL server")
	}

	schema := fmt.Sprintf("pui_sub_test_%d_%d", os.Getpid(), subTestSchemaSeq.Add(1))
	if err := execOnSubDSN(dsn, `CREATE SCHEMA "`+schema+`"`); err != nil {
		t.Fatalf("create test schema %s: %v", schema, err)
	}
	t.Cleanup(func() {
		_ = database.CloseDB()
		if err := execOnSubDSN(dsn, `DROP SCHEMA IF EXISTS "`+schema+`" CASCADE`); err != nil {
			t.Logf("drop test schema %s: %v", schema, err)
		}
	})

	t.Setenv("PUI_DB_DSN", subDSNWithSearchPath(dsn, schema))
	if err := database.InitDB(); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
}

// execOnSubDSN runs one statement on a short-lived connection, used for the
// schema bookkeeping that has to happen outside the panel's own connection.
func execOnSubDSN(dsn, statement string) error {
	gdb, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Discard})
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

// subDSNWithSearchPath pins a DSN to one schema, in whichever of the two shapes
// pgx accepts the caller wrote it.
func subDSNWithSearchPath(dsn, schema string) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		separator := "?"
		if strings.Contains(dsn, "?") {
			separator = "&"
		}
		return dsn + separator + "search_path=" + schema
	}
	return dsn + " search_path=" + schema
}
