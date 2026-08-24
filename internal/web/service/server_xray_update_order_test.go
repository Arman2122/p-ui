package service

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// xrayZip builds a release archive the download path will accept, plus the
// .dgst sidecar it is verified against.
func xrayZip(t *testing.T, payload string) (archive []byte, digest string) {
	t.Helper()
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	f, err := w.Create("xray")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

/*
A failed update must not leave the box with xray stopped.

UpdateXray stopped the process first and then downloaded, so every failure below
that point — a 404, a digest mismatch, a connection dropped mid-copy — returned
bare and left the proxy down with the old binary still in place. On a filtered
network it was worse than "sometimes": stopping xray also removes the panel's
own egress bridge, because PanelEgressProxyURL answers "" while the process is
not running, so the operator's configured proxy was unavailable exactly when the
download needed it.

Nothing here starts a real core; the assertion is that the failure path does not
touch the installed binary, which is what "the box is still serving" reduces to
once the stop is gone.
*/
func TestAFailedXrayUpdateLeavesTheInstalledBinaryAlone(t *testing.T) {
	// NewProxiedHTTPClient reads the panel-egress setting on its way out.
	initTestDB(t)
	binDir := t.TempDir()
	t.Setenv("PUI_BIN_FOLDER", binDir)
	const original = "the xray that is currently running"
	installed := filepath.Join(binDir, "xray")
	if err := os.WriteFile(installed, []byte(original), 0o755); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
	}{
		{
			name: "the release asset does not exist for this architecture",
			handler: func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, "not found", http.StatusNotFound)
			},
		},
		{
			name: "the archive does not match its published digest",
			handler: func(w http.ResponseWriter, r *http.Request) {
				body, _ := xrayZip(t, "a different binary entirely")
				if strings.HasSuffix(r.URL.Path, ".dgst") {
					_, _ = fmt.Fprintf(w, "SHA2-256= %s\n", strings.Repeat("0", 64))
					return
				}
				_, _ = w.Write(body)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()
			restore := xrayReleaseBaseURL
			xrayReleaseBaseURL = srv.URL
			defer func() { xrayReleaseBaseURL = restore }()

			if _, err := (&ServerService{}).downloadXRay("v1.0.0"); err == nil {
				t.Fatal("the download reported success")
			}

			got, err := os.ReadFile(installed)
			if err != nil {
				t.Fatalf("the installed binary is gone: %v", err)
			}
			if string(got) != original {
				t.Fatalf("the installed binary was replaced by a failed update: %q", got)
			}
		})
	}
}

/*
And the ordering itself: the download must happen while xray is still running,
because it is xray that carries the panel's egress proxy on a filtered network.

Asserted structurally — UpdateXray must not stop the process itself. Its only
lifecycle call is RestartXray at the end, which runs the -test pre-flight before
it stops anything, so a binary that verifies but cannot load this config is
refused with the old core still serving.
*/
func TestUpdateXrayNeverStopsTheCoreItself(t *testing.T) {
	body, err := os.ReadFile("server.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(body)
	start := strings.Index(src, "func (s *ServerService) UpdateXray(version string) error {")
	if start < 0 {
		t.Fatal("UpdateXray not found; this guard is now vacuous")
	}
	end := strings.Index(src[start:], "\nfunc ")
	if end < 0 {
		t.Fatal("could not find the end of UpdateXray")
	}
	fn := src[start : start+end]

	if strings.Contains(fn, "StopXrayService()") {
		t.Fatal("UpdateXray stops the core itself; every failure after that point leaves the box down, and it removes the egress bridge the download needs")
	}
	// The call form, not the bare name: the doc comment above it says the same
	// words, and matching prose would make the ordering check meaningless.
	restart := strings.Index(fn, "s.xrayService.RestartXray(true)")
	if restart < 0 {
		t.Fatal("UpdateXray no longer restarts the core, so a downloaded binary is never picked up")
	}
	download := strings.Index(fn, "s.downloadXRay(")
	if download < 0 || download > restart {
		t.Fatal("the download does not run before the restart; it must happen while the old core is still serving, because the panel's egress proxy is hosted by it")
	}
}
