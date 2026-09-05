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
	"runtime"
	"strings"
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
	return New(Deps{HTTPClient: func(time.Duration) *http.Client { return srv.Client() }})
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
	if runtime.GOOS == "windows" {
		return
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
