//go:build linux

package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

/*
A restore keeps the database it is about to overwrite.

ImportDB checked that pg_restore could read the upload, stopped Xray and
restored. Nothing was taken first, so restoring the wrong file -- an old
backup, another panel's -- was the one panel operation with no way back. The
current database is spooled beside the dump spool before the restore starts,
only the latest such file is kept, and a dump that fails stops the restore.
Feature gap O9.
*/
func TestTheRestoreKeepsOnlyTheLatestPreRestoreDump(t *testing.T) {
	fakePgDump(t, "printf 'PGDMP-before'")
	spool := dumpSpoolDir()
	existing, _ := filepath.Glob(filepath.Join(spool, "p-ui-pre-restore-*.dump"))
	for _, path := range existing {
		_ = os.Remove(path)
	}

	first, err := (&ServerService{}).keepPreRestoreDump()
	if err != nil {
		t.Fatalf("keepPreRestoreDump: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(first) })
	body, err := os.ReadFile(first)
	if err != nil || string(body) != "PGDMP-before" {
		t.Fatalf("safety dump = %q, %v; want the database as pg_dump produced it", body, err)
	}

	fakePgDump(t, "printf 'PGDMP-later'")
	second, err := (&ServerService{}).keepPreRestoreDump()
	if err != nil {
		t.Fatalf("second keepPreRestoreDump: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(second) })
	kept, _ := filepath.Glob(filepath.Join(spool, "p-ui-pre-restore-*.dump"))
	if len(kept) != 1 || kept[0] != second {
		t.Fatalf("pre-restore dumps on disk = %v, want only the latest (%s)", kept, second)
	}
}

// ImportDB must take the safety dump before it stops Xray and closes the
// database, or the dump is of nothing. Pinned on the source, as the tgbot
// guard is: a live restore needs pg_restore and a database to overwrite.
func TestImportDBTakesTheSafetyDumpBeforeStoppingAnything(t *testing.T) {
	body, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatalf("read server.go: %v", err)
	}
	src := string(body)
	start := strings.Index(src, "func (s *ServerService) ImportDB(")
	if start < 0 {
		t.Fatal("ImportDB was renamed; this guard is vacuous")
	}
	fn := src[start:]
	dump := strings.Index(fn, "s.keepPreRestoreDump()")
	stop := strings.Index(fn, "s.StopXrayService()")
	if dump < 0 || stop < 0 || dump > stop {
		t.Fatalf("ImportDB takes the safety dump at offset %d and stops Xray at %d: the dump must come first", dump, stop)
	}
}
