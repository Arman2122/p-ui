package xray

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	engine "github.com/Arman2122/p-ui/internal/xray"
)

// A real XTLS .dgst sidecar (Xray-linux-64.zip.dgst, v26.3.27): lines are
// "ALGO= <hex>", and the algorithm label is "SHA2-256", not "SHA256".
const sampleDgst = `# Hash Values

MD5= ee4e2ff74948a9b464624b1cabc44409
SHA1= b55b06e74e89083b9cedfdecf0d68b579cd2af72
SHA2-256= 23cd9af937744d97776ee35ecad4972cf4b2109d1e0fe6be9930467608f7c8ae
SHA2-512= e8bc40a0687cac184bbe4b5c1f047e69064ccedc489fb25e208889ae287bbf8736dff16b108d68fc00dc33edc8bb53502e47a9698a277f4f51b67b83d899e518
`

const wantSHA = "23cd9af937744d97776ee35ecad4972cf4b2109d1e0fe6be9930467608f7c8ae"

func TestParseDigestSHA256(t *testing.T) {
	got, err := parseDigestSHA256([]byte(sampleDgst))
	if err != nil || got != wantSHA {
		t.Fatalf("parse = %q, %v; want %q", got, err, wantSHA)
	}
	for _, tc := range []struct{ name, in string }{
		{"no-sha256-line", "MD5= abc\nSHA1= def\n"},
		{"malformed-short", "SHA2-256= deadbeef\n"},
		{"empty", ""},
	} {
		if _, err := parseDigestSHA256([]byte(tc.in)); err == nil {
			t.Errorf("%s: expected an error", tc.name)
		}
	}
}

func TestFetchDigestSHA256(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "missing.dgst") {
			http.Error(w, "nope", http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(sampleDgst))
	}))
	defer srv.Close()
	got, err := fetchDigest(context.Background(), srv.Client(), srv.URL+"/Xray-linux-64.zip.dgst")
	if err != nil || got != wantSHA {
		t.Fatalf("fetch = %q, %v; want %q", got, err, wantSHA)
	}
	if _, err := fetchDigest(context.Background(), srv.Client(), srv.URL+"/missing.dgst"); err == nil {
		t.Fatal("expected an error on HTTP 404")
	}
}

// xrayZip builds a release archive the download path will accept, plus the
// digest it is verified against.
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

// releaseServer serves a release list and, per version, the archive and its
// digest, the way GitHub does; tests point the package vars at it.
func releaseServer(t *testing.T, tags []string, archive []byte, digest string) *Core {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/releases":
			w.Header().Set("Content-Type", "application/json")
			parts := make([]string, len(tags))
			for i, tag := range tags {
				parts[i] = fmt.Sprintf(`{"tag_name":%q}`, tag)
			}
			_, _ = io.WriteString(w, "["+strings.Join(parts, ",")+"]")
		case strings.HasSuffix(r.URL.Path, ".dgst"):
			_, _ = fmt.Fprintf(w, "SHA2-256= %s\n", digest)
		case archive == nil:
			http.Error(w, "not found", http.StatusNotFound)
		default:
			_, _ = w.Write(archive)
		}
	}))
	t.Cleanup(srv.Close)
	previousList, previousBase := releasesURL, downloadBase
	releasesURL, downloadBase = srv.URL+"/releases", srv.URL
	t.Cleanup(func() { releasesURL, downloadBase = previousList, previousBase })
	c := New(Deps{HTTPClient: func(time.Duration) *http.Client { return srv.Client() }})
	c.mgr = &engine.Manager{}
	return c
}

/*
The GitHub release list is read under a cap, like every other body the panel
fetches (F285), and the filter keeps what the panel's config generator can
drive: 26.6.27 and newer, tag included.
*/
func TestAvailableCapsTheListAndKeepsLoadableReleases(t *testing.T) {
	huge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "[")
		for i := 0; i <= maxReleasesBytes/24; i++ {
			_, _ = fmt.Fprintf(w, `{"tag_name":"v25.%d.0"},`, i)
		}
		_, _ = io.WriteString(w, `{"tag_name":"v25.9.9"}]`)
	}))
	t.Cleanup(huge.Close)
	previous := releasesURL
	t.Cleanup(func() { releasesURL = previous })
	releasesURL = huge.URL
	c := New(Deps{HTTPClient: func(time.Duration) *http.Client { return huge.Client() }})
	if _, err := c.Available(context.Background()); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("a release list over the cap: err = %v, want a refusal naming the cap", err)
	}

	c = releaseServer(t, []string{"v26.7.1", "v26.6.27", "v25.9.9", "v27.0.0-rc1", "nightly"}, nil, "")
	got, err := c.Available(context.Background())
	if err != nil {
		t.Fatalf("a normal release list: %v", err)
	}
	if strings.Join(got, ",") != "v26.7.1,v26.6.27" {
		t.Fatalf("versions = %v, want [v26.7.1 v26.6.27]", got)
	}
}

/*
A failed install must not touch the installed binary: a 404, a digest mismatch
or a version the channel never listed all return with the old core still the
one on disk, so nothing the restart afterwards could do makes the box worse.
*/
func TestAFailedInstallLeavesTheInstalledBinaryAlone(t *testing.T) {
	binDir := t.TempDir()
	t.Setenv("PUI_BIN_FOLDER", binDir)
	const original = "the xray that is currently running"
	installed := engine.GetBinaryPath()
	if err := os.WriteFile(installed, []byte(original), 0o755); err != nil {
		t.Fatal(err)
	}
	wrong, _ := xrayZip(t, "a different binary entirely")
	for _, tc := range []struct {
		name    string
		core    *Core
		version string
		want    error
	}{
		{"the asset does not exist for this architecture", releaseServer(t, []string{"v26.7.1"}, nil, ""), "v26.7.1", nil},
		{"the archive does not match its published digest", releaseServer(t, []string{"v26.7.1"}, wrong, strings.Repeat("0", 64)), "v26.7.1", nil},
		{"the version is not one the channel lists", releaseServer(t, []string{"v26.7.1"}, wrong, ""), "v26.7.2", core.ErrUnknownVersion},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.core.Install(context.Background(), tc.version)
			if err == nil {
				t.Fatal("the install reported success")
			}
			if tc.want != nil && !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			got, err := os.ReadFile(installed)
			if err != nil || string(got) != original {
				t.Fatalf("the installed binary is %q, %v; a failed install replaced it", got, err)
			}
			if stray, _ := filepath.Glob(filepath.Join(binDir, ".xray-*")); len(stray) != 0 {
				t.Fatalf("a failed install left %v staged beside the binary", stray)
			}
		})
	}
}

// A listed, verified release lands: the member is staged beside the binary and
// renamed over it, and nothing is restarted. Installed then names the new one.
func TestInstallLandsAVerifiedReleaseWithoutRestarting(t *testing.T) {
	requirePosixHost(t)
	binDir := t.TempDir()
	t.Setenv("PUI_BIN_FOLDER", binDir)
	installed := engine.GetBinaryPath()
	if err := os.WriteFile(installed, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := "#!/bin/sh\necho \"Xray 99.0.0 (fake)\"\n"
	archive, digest := xrayZip(t, payload)
	c := releaseServer(t, []string{"v99.0.0"}, archive, digest)
	if err := c.Install(context.Background(), "v99.0.0"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	got, err := os.ReadFile(installed)
	if err != nil || string(got) != payload {
		t.Fatalf("installed binary = %q, %v; want the release's member", got, err)
	}
	if info, err := os.Stat(installed); err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Fatalf("the installed binary is not executable: %v, %v", info, err)
	}
	if v, err := c.Installed(context.Background()); err != nil || v != "99.0.0" {
		t.Fatalf("Installed = %q, %v; want the version the new binary reports", v, err)
	}
}

// A core the admin never used has no binary, which is a state and not a fault.
func TestInstalledNamesAMissingBinary(t *testing.T) {
	t.Setenv("PUI_BIN_FOLDER", t.TempDir())
	if _, err := New(Deps{}).Installed(context.Background()); !errors.Is(err, core.ErrNotInstalled) {
		t.Fatalf("Installed with no binary = %v, want ErrNotInstalled", err)
	}
}

func TestXrayUpdaterAvailableStableOnly(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `[
			{"tag_name":"v27.0.0","draft":true},
			{"tag_name":"v26.9.0","prerelease":true},
			{"tag_name":"v27.-1.0"},{"tag_name":"v27.0.-1"},
			{"tag_name":"v-27.0.0"},{"tag_name":"v+27.0.0"},
			{"tag_name":"27.0.0"},{"tag_name":"v27.01.0"},
			{"tag_name":"v27.0.0-rc1"},{"tag_name":"v27.0.0+build"},
			{"tag_name":"v999999999999999999999999.0.0"},
			{"tag_name":"v26.8.1"},{"tag_name":"v26.6.27"},{"tag_name":"v26.6.26"}
		]`)
	}))
	t.Cleanup(srv.Close)
	previous := releasesURL
	releasesURL = srv.URL
	t.Cleanup(func() { releasesURL = previous })
	c := New(Deps{HTTPClient: func(time.Duration) *http.Client { return srv.Client() }})
	versions, err := c.Available(t.Context())
	if err != nil || strings.Join(versions, ",") != "v26.8.1,v26.6.27" {
		t.Fatalf("Available = %v, %v; want only [v26.8.1 v26.6.27]", versions, err)
	}
}

func TestXrayUpdaterRejectsUnstableInstallBeforeDownload(t *testing.T) {
	for _, tag := range []string{"v27.-1.0", "v27.0.-1", "27.0.0", "v27.01.0"} {
		t.Run(tag, func(t *testing.T) {
			var downloads atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/releases" {
					_, _ = fmt.Fprintf(w, `[{"tag_name":%q}]`, tag)
					return
				}
				downloads.Add(1)
				http.NotFound(w, r)
			}))
			t.Cleanup(srv.Close)
			oldList, oldBase := releasesURL, downloadBase
			releasesURL, downloadBase = srv.URL+"/releases", srv.URL
			t.Cleanup(func() { releasesURL, downloadBase = oldList, oldBase })
			c := New(Deps{HTTPClient: func(time.Duration) *http.Client { return srv.Client() }})
			if err := c.Install(t.Context(), tag); !errors.Is(err, core.ErrUnknownVersion) {
				t.Fatalf("Install(%q) = %v; want ErrUnknownVersion", tag, err)
			}
			if got := downloads.Load(); got != 0 {
				t.Fatalf("download requests = %d; want none", got)
			}
		})
	}
}

func assertXrayInstalledBytes(t *testing.T, original string) {
	t.Helper()
	got, err := os.ReadFile(engine.GetBinaryPath())
	if err != nil || string(got) != original {
		t.Fatalf("installed binary = %q, %v; want original %q", got, err, original)
	}
	staged, err := filepath.Glob(filepath.Join(filepath.Dir(engine.GetBinaryPath()), ".xray-*"))
	if err != nil || len(staged) != 0 {
		t.Fatalf("staged files = %v, %v; want none", staged, err)
	}
}

func seedXrayBinary(t *testing.T, contents string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("PUI_BIN_FOLDER", dir)
	t.Setenv("PUI_LOG_FOLDER", dir)
	if err := os.WriteFile(engine.GetBinaryPath(), []byte(contents), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestXrayUpdaterCandidateValidationWithoutConfig(t *testing.T) {
	requirePosixHost(t)
	for _, tc := range []struct{ name, payload, want string }{
		{"wrong-version", "#!/bin/sh\necho 'Xray 99.0.1 (test)'\n", "does not match"},
		{"wrong-program", "#!/bin/sh\necho 'Other 99.0.0 (test)'\n", "unexpected answer"},
		{"malformed-version", "#!/bin/sh\necho 'Xray 99.-1.0 (test)'\n", "unexpected answer"},
		{"not-executable", "not an executable file", "executable validation"},
		{"failed-version-command", "#!/bin/sh\necho 'Xray 99.0.0 (test)'\nexit 7\n", "executable validation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seedXrayBinary(t, "old installed bytes")
			archive, digest := xrayZip(t, tc.payload)
			c := releaseServer(t, []string{"v99.0.0"}, archive, digest)
			err := c.Install(t.Context(), "v99.0.0")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Install = %v; want %q", err, tc.want)
			}
			assertXrayInstalledBytes(t, "old installed bytes")
		})
	}
}

func TestXrayUpdaterInstalledReadsDiskWhileRunning(t *testing.T) {
	requirePosixHost(t)
	seedXrayBinary(t, "#!/bin/sh\nif [ \"$1\" = '-version' ]; then echo 'Xray 26.7.28 (test)'; exit 0; fi\nexec sleep 60\n")
	p := engine.NewTestProcess(&engine.Config{}, filepath.Join(filepath.Dir(engine.GetBinaryPath()), "test-running.json"))
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = p.Stop() })
	if !p.IsRunning() || p.GetXrayVersion() != "26.7.28" {
		t.Fatalf("running = %v, version = %q", p.IsRunning(), p.GetXrayVersion())
	}
	candidate := filepath.Join(filepath.Dir(engine.GetBinaryPath()), "replacement")
	if err := os.WriteFile(candidate, []byte("#!/bin/sh\necho 'Xray 99.0.0 (test)'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(candidate, engine.GetBinaryPath()); err != nil {
		t.Fatal(err)
	}
	c := New(Deps{})
	c.mgr = &engine.Manager{}
	c.mgr.Replace(p)
	if got, err := c.Installed(t.Context()); err != nil || got != "99.0.0" {
		t.Fatalf("Installed = %q, %v; want on-disk 99.0.0", got, err)
	}
	if !p.IsRunning() || p.GetXrayVersion() != "26.7.28" {
		t.Fatal("reporting the installed version changed the running process")
	}
}

func TestXrayUpdaterPreservesActiveConfigValidation(t *testing.T) {
	requirePosixHost(t)
	seedXrayBinary(t, "old installed bytes")
	payload := "#!/bin/sh\nif [ \"$1\" = '-version' ]; then echo 'Xray 99.0.0 (test)'; exit 0; fi\necho 'Failed to start: candidate rejects the active config'\nexit 23\n"
	archive, digest := xrayZip(t, payload)
	c := releaseServer(t, []string{"v99.0.0"}, archive, digest)
	c.mgr.Replace(engine.NewProcess(&engine.Config{}))
	if err := c.Install(t.Context(), "v99.0.0"); err == nil || !strings.Contains(err.Error(), "candidate rejects the active config") {
		t.Fatalf("Install = %v; want active config refusal", err)
	}
	assertXrayInstalledBytes(t, "old installed bytes")
}

func waitForXrayValidation(t *testing.T, marker string, result <-chan error) {
	t.Helper()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(marker); err == nil {
			return
		}
		select {
		case err := <-result:
			t.Fatalf("Install finished before candidate validation: %v", err)
		case <-deadline.C:
			t.Fatal("candidate validation was not reached")
		case <-tick.C:
		}
	}
}

func TestXrayUpdaterCancellationPreservesBinary(t *testing.T) {
	requirePosixHost(t)
	for _, configCheck := range []bool{false, true} {
		t.Run(fmt.Sprintf("active-config=%v", configCheck), func(t *testing.T) {
			seedXrayBinary(t, "old installed bytes")
			dir := filepath.Dir(engine.GetBinaryPath())
			marker, release := filepath.Join(dir, "checked"), filepath.Join(dir, "release")
			t.Setenv("PUI_XRAY_TEST_MARKER", marker)
			t.Setenv("PUI_XRAY_TEST_RELEASE", release)
			payload := "#!/bin/sh\n: > \"$PUI_XRAY_TEST_MARKER\"\nexec sleep 60\n"
			if configCheck {
				payload = "#!/bin/sh\nif [ \"$1\" = '-version' ]; then echo 'Xray 99.0.0 (test)'; exit 0; fi\n: > \"$PUI_XRAY_TEST_MARKER\"\nwhile [ ! -f \"$PUI_XRAY_TEST_RELEASE\" ]; do sleep 0.01; done\nexit 0\n"
			}
			archive, digest := xrayZip(t, payload)
			c := releaseServer(t, []string{"v99.0.0"}, archive, digest)
			if configCheck {
				c.mgr.Replace(engine.NewProcess(&engine.Config{}))
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o600) })
			result := make(chan error, 1)
			go func() { result <- c.Install(ctx, "v99.0.0") }()
			waitForXrayValidation(t, marker, result)
			cancel()
			if err := os.WriteFile(release, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-result:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("Install = %v; want context.Canceled", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("cancelled Install did not finish")
			}
			assertXrayInstalledBytes(t, "old installed bytes")
		})
	}
}

func TestInstallRefusesAConcurrentInstall(t *testing.T) {
	c := &Core{}
	c.updateMu.Lock()
	defer c.updateMu.Unlock()
	if err := c.Install(context.Background(), "v26.7.28"); err == nil || !strings.Contains(err.Error(), "already in progress") {
		t.Fatalf("concurrent Install = %v, want busy refusal", err)
	}
}
