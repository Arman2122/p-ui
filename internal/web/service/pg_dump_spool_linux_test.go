//go:build linux

package service

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

/*
The database dump is spooled to disk, not collected in RAM.

exportPostgresDB read the whole pg_dump output into a bytes.Buffer and handed
it back as []byte, so a panel needed at least the size of its own dump free in
memory to take a backup, and a failed dump surfaced only as an error string.
pg_dump writes to a disk-backed file now, the exit status is checked before
the file is served, and a failure leaves nothing behind. F276.
*/
func fakePgDump(t *testing.T, script string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pg_dump"), []byte("#!/bin/sh\n"+script+"\n"), 0o755); err != nil {
		t.Fatalf("write fake pg_dump: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PUI_DB_DSN", "postgres://u:p@127.0.0.1:5432/db?sslmode=disable")
}

func TestTheDumpIsSpooledToDiskNotRAM(t *testing.T) {
	const size = 32 << 20
	fakePgDump(t, "head -c 33554432 /dev/zero")

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	dump, err := (&ServerService{}).exportPostgresDB()
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatalf("exportPostgresDB: %v", err)
	}
	if dump.Size != size {
		t.Fatalf("dump size = %d, want %d", dump.Size, size)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew > 8<<20 {
		t.Fatalf("taking a 32 MiB dump allocated %d bytes on the heap: it is buffered, not spooled", grew)
	}
	name := dump.Name()
	if err := dump.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if _, err := os.Stat(name); err == nil {
		t.Fatal("Close did not remove the spool file")
	}
}

func TestAFailedDumpLeavesNothingBehind(t *testing.T) {
	fakePgDump(t, "head -c 1048576 /dev/zero; echo boom >&2; exit 1")
	spooled := func() int {
		matches, _ := filepath.Glob(filepath.Join(dumpSpoolDir(), "p-ui-pg-dump-*.dump"))
		return len(matches)
	}
	before := spooled()

	_, err := (&ServerService{}).exportPostgresDB()
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("err = %v, want pg_dump's own stderr in the refusal", err)
	}
	if spooled() != before {
		t.Fatal("a failed dump left its spool file behind")
	}
}
