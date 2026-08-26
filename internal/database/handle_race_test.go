package database

import (
	"os"
	"strings"
	"sync"
	"testing"

	"gorm.io/gorm"
)

/*
The package handle is swapped at runtime, so every access has to be guarded.

A database restore closes the pool and re-opens it while eighteen cron jobs are
calling GetDB(). The pointer was a bare global with no synchronisation anywhere
in this package, which is a data race by definition — and the Go memory model
makes no promise about which handle a reader sees. F274.

Run under -race this fails on the unguarded shape. Without -race it still
exercises the lock, so it is not a no-op in the ordinary suite.
*/
func TestTheHandleCanBeReplacedWhileItIsBeingRead(t *testing.T) {
	original := GetDB()
	t.Cleanup(func() { setDB(original) })

	const readers = 8
	var wg sync.WaitGroup
	ready := make(chan struct{})
	stop := make(chan struct{})

	for range readers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-ready // spin only once the writer is about to swap
			for {
				select {
				case <-stop:
					return
				default:
					_ = GetDB()
				}
			}
		}()
	}

	replacement := &gorm.DB{}
	close(ready)
	for range 50_000 {
		setDB(replacement)
		setDB(original)
	}
	close(stop)
	wg.Wait()

	if GetDB() != original {
		t.Fatal("the handle was not restored; the test left the package in a broken state")
	}
}

/*
And a failed re-open must not replace a working handle with nil.

InitDB assigned the result of its open unconditionally, so a restore whose
re-open failed stored nil — turning every handler and cron job into a
nil-pointer panic. Keeping the stale pool gives "sql: database is closed"
instead: still broken until a restart, but diagnosable rather than a panic
storm, and the restore path now says so in its own error. F275.

Asserted on the source. gorm.Open connects lazily, so a DSN pointing at nothing
still "succeeds" here — the failure this guards against needs a real unreachable
server at exactly the wrong moment, which a unit test cannot stage and which is
precisely why the ordering has to be readable instead.
*/
func TestInitDBDoesNotStoreAHandleItFailedToOpen(t *testing.T) {
	body, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go: %v", err)
	}
	source := string(body)

	open := strings.Index(source, "conn, err := openPostgresWithRetry(dsn,")
	if open < 0 {
		t.Fatal("InitDB no longer opens through openPostgresWithRetry; this guard is now vacuous")
	}
	tail := source[open:]
	guard := strings.Index(tail, "if err != nil {")
	store := strings.Index(tail, "setDB(conn)")
	if guard < 0 || store < 0 {
		t.Fatal("InitDB no longer guards or no longer stores; this guard is now vacuous")
	}
	if store < guard {
		t.Fatal("InitDB stores the handle before checking the error: a failed re-open replaces a working pool with nil, and every handler and cron job panics instead of returning an error")
	}
}
