package panel

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

/*
The updater must come from the release it installs, and from nowhere else.

downloadPanelUpdater fetched raw.githubusercontent.com/.../main/update.sh and
handed it to root bash on every host -- the panel's Update button, `p-ui
update` and node remote-update all went through it. A commit on main was code
execution everywhere. F277.

Needs the database only because the proxied HTTP client reads the panel's
egress-proxy setting; nothing here touches a row.
*/
func TestPanelUpdaterIsFetchedFromTheReleaseTag(t *testing.T) {
	initTestDB(t)

	var mu sync.Mutex
	var served []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		served = append(served, r.URL.Path)
		mu.Unlock()
		if r.URL.Path != "/v9.9.9/update.sh" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("#!/bin/bash\necho released updater\n"))
	}))
	defer srv.Close()

	previous := panelReleaseBaseURL
	panelReleaseBaseURL = srv.URL + "/"
	t.Cleanup(func() { panelReleaseBaseURL = previous })

	path, err := downloadPanelUpdater("v9.9.9")
	if err != nil {
		t.Fatalf("downloadPanelUpdater(v9.9.9): %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(body), "released updater") {
		t.Fatalf("script = %q, want the body the release served", body)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(served) != 1 || served[0] != "/v9.9.9/update.sh" {
		t.Fatalf("served = %v, want exactly one request for the tagged updater", served)
	}
	for _, p := range served {
		if strings.Contains(p, "/main/") {
			t.Fatalf("served = %v: the updater was fetched from the main branch again", served)
		}
	}
}

// A release that publishes no updater is refused outright. Falling back to main
// here would reopen the hole for exactly the releases that predate the fix.
func TestPanelUpdaterRefusesAReleaseWithoutOne(t *testing.T) {
	initTestDB(t)

	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()

	previous := panelReleaseBaseURL
	panelReleaseBaseURL = srv.URL + "/"
	t.Cleanup(func() { panelReleaseBaseURL = previous })

	path, err := downloadPanelUpdater("v1.5.1")
	if err == nil {
		_ = os.Remove(path)
		t.Fatal("a release with no update.sh asset was accepted; want a refusal, never a fallback")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Fatalf("err = %v, want the HTTP 404 named so the operator sees which release lacks it", err)
	}
}
