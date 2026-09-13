package mtproto

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	engine "github.com/Arman2122/p-ui/internal/mtproto"
)

// mtgTarball builds the archive the channel publishes: one directory named
// like the package, the binary inside it.
func mtgTarball(t *testing.T, pkg, payload string) (archive []byte, sum string) {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: pkg + "/mtg-proxy", Mode: 0o755, Size: int64(len(payload)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(payload)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(digest[:])
}

// channel serves one release the way GitHub does: the list, the tarball and the
// checksums file; sum is what the checksums file claims for the tarball.
func channel(t *testing.T, tag string, archive []byte, sum string, olderTags ...string) *Core {
	t.Helper()
	bare := strings.TrimPrefix(tag, "v")
	listed := fmt.Sprintf(`{"tag_name":%q},{"tag_name":"v0.0.1-rc1","prerelease":true},{"tag_name":"v9.9.8","draft":true},{"tag_name":"dev-2af4639"},{"tag_name":"v9.9.9-rc.1"}`, tag)
	for _, older := range olderTags {
		listed += fmt.Sprintf(`,{"tag_name":%q}`, older)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/releases":
			_, _ = fmt.Fprintf(w, "[%s]", listed)
		case strings.HasSuffix(r.URL.Path, "-checksums.txt"):
			_, _ = fmt.Fprintf(w, "%s  mtg-proxy-%s-linux-amd64.tar.gz\n%s  mtg-proxy-%s-sbom.spdx.json\n", sum, bare, strings.Repeat("1", 64), bare)
		case strings.HasSuffix(r.URL.Path, ".tar.gz"):
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	previousList, previousBase := releasesURL, downloadBase
	releasesURL, downloadBase = srv.URL+"/releases", srv.URL
	t.Cleanup(func() { releasesURL, downloadBase = previousList, previousBase })
	return New(Deps{Platform: "amd64", HTTPClient: func(time.Duration) *http.Client { return srv.Client() }})
}

func TestParseChecksumFindsTheNamedAsset(t *testing.T) {
	sums := []byte("9bd8b33c9adf4d68f18494c5bd600ba695ea78fdcf55cb696ee68b5034ab1607  mtg-proxy-1.3.0-linux-386.tar.gz\nedf40e18dd417a59a4624ef14b6fabc25cc5b77fc11d0dcc63a0a21dd42fc566  mtg-proxy-1.3.0-linux-amd64.tar.gz\n")
	got, err := parseChecksum(sums, "mtg-proxy-1.3.0-linux-amd64.tar.gz")
	if err != nil || got != "edf40e18dd417a59a4624ef14b6fabc25cc5b77fc11d0dcc63a0a21dd42fc566" {
		t.Fatalf("parseChecksum = %q, %v", got, err)
	}
	if _, err := parseChecksum(sums, "mtg-proxy-1.3.0-linux-armv7.tar.gz"); err == nil {
		t.Fatal("an asset the file does not list must not verify")
	}
}

// Available drops pre-releases; Install refuses what Available did not list.
func TestAvailableListsReleasesAndInstallRefusesTheRest(t *testing.T) {
	t.Setenv("PUI_BIN_FOLDER", t.TempDir())
	c := channel(t, "v9.9.9", nil, "")
	got, err := c.Available(context.Background())
	if err != nil || strings.Join(got, ",") != "v9.9.9" {
		t.Fatalf("Available = %v, %v; want [v9.9.9] with the pre-release left out", got, err)
	}
	for _, version := range []string{"v9.9.8", "dev-2af4639", "v9.9.9-rc.1"} {
		if err := c.Install(context.Background(), version); !errors.Is(err, core.ErrUnknownVersion) {
			t.Fatalf("Install(%s) = %v, want ErrUnknownVersion", version, err)
		}
	}
	if _, err := c.Installed(context.Background()); !errors.Is(err, core.ErrNotInstalled) {
		t.Fatalf("Installed with no binary = %v, want ErrNotInstalled", err)
	}
}

func TestPublishedChecksumValidatesHex(t *testing.T) {
	const asset = "mtg-proxy-9.9.9-linux-amd64.tar.gz"
	for _, value := range []string{strings.Repeat("A", 64), strings.Repeat("z", 64), strings.Repeat("0", 63)} {
		t.Run(value[:2]+fmt.Sprint(len(value)), func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(w, "%s  %s\n", value, asset)
			}))
			defer srv.Close()
			got, err := publishedSum(context.Background(), srv.Client(), srv.URL, asset)
			if value == strings.Repeat("A", 64) {
				if err != nil || got != strings.ToLower(value) {
					t.Fatalf("publishedSum = %q, %v; want normalized hexadecimal checksum", got, err)
				}
				return
			}
			want := "mtproto: checksums: malformed entry for " + asset
			if got != "" || err == nil || err.Error() != want {
				t.Fatalf("publishedSum = %q, %v; want empty, %q", got, err, want)
			}
		})
	}
}

func TestInstallRejectsDifferentReportedVersion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("staged shell binary requires a POSIX host")
	}
	for _, reported := range []string{"9.9.8", "10.0.0", "dev-2af4639"} {
		t.Run(reported, func(t *testing.T) {
			t.Setenv("PUI_BIN_FOLDER", t.TempDir())
			installed := engine.GetBinaryPath()
			if err := os.WriteFile(installed, []byte("known-good"), 0o755); err != nil {
				t.Fatal(err)
			}
			payload := "#!/bin/sh\necho '" + reported + " (go1.26 test)'\n"
			archive, sum := mtgTarball(t, "mtg-proxy-9.9.9-linux-amd64", payload)
			c := channel(t, "v9.9.9", archive, sum)
			err := c.Install(context.Background(), "v9.9.9")
			if err == nil || !strings.Contains(err.Error(), "does not match requested v9.9.9") || !strings.Contains(err.Error(), reported) {
				t.Fatalf("Install = %v; want requested/reported version mismatch", err)
			}
			if got, err := os.ReadFile(installed); err != nil || string(got) != "known-good" {
				t.Fatalf("installed = %q, %v; version mismatch replaced good binary", got, err)
			}
			if staged, _ := filepath.Glob(filepath.Join(filepath.Dir(installed), ".mtg-*")); len(staged) != 0 {
				t.Fatalf("refused install left staged binaries: %v", staged)
			}
		})
	}
}

func TestAtomicReplacementRejectsCanceledInstall(t *testing.T) {
	dir := t.TempDir()
	installed, staged := filepath.Join(dir, "mtg"), filepath.Join(dir, ".mtg-staged")
	if err := os.WriteFile(installed, []byte("known-good"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte("verified-new"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := replaceStagedBinary(ctx, staged, installed); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled replacement = %v; want context.Canceled", err)
	}
	if got, err := os.ReadFile(installed); err != nil || string(got) != "known-good" {
		t.Fatalf("installed = %q, %v; canceled commit replaced good binary", got, err)
	}
	if err := replaceStagedBinary(context.Background(), staged, installed); err != nil {
		t.Fatalf("uncanceled replacement: %v", err)
	}
	if got, err := os.ReadFile(installed); err != nil || string(got) != "verified-new" {
		t.Fatalf("installed = %q, %v; want verified replacement", got, err)
	}
}

// Failed checksum or platform validation leaves the installed binary intact and no staging files.
func TestAFailedInstallLeavesTheInstalledBinaryAlone(t *testing.T) {
	binDir := t.TempDir()
	t.Setenv("PUI_BIN_FOLDER", binDir)
	const original = "the mtg that is currently running"
	installed := engine.GetBinaryPath()
	if err := os.WriteFile(installed, []byte(original), 0o755); err != nil {
		t.Fatal(err)
	}
	archive, _ := mtgTarball(t, "mtg-proxy-9.9.9-linux-amd64", "not what the checksum says")
	mismatched := channel(t, "v9.9.9", archive, strings.Repeat("0", 64))
	if err := mismatched.Install(context.Background(), "v9.9.9"); err == nil || !strings.Contains(err.Error(), "checksum") {
		t.Fatalf("Install with a wrong checksum = %v; want a refusal naming the checksum", err)
	}
	unstamped := channel(t, "v9.9.9", archive, strings.Repeat("0", 64))
	unstamped.deps.Platform = ""
	if err := unstamped.Install(context.Background(), "v9.9.9"); err == nil || !strings.Contains(err.Error(), "platform") {
		t.Fatalf("Install on an unstamped build = %v; want a refusal to guess the asset", err)
	}
	got, err := os.ReadFile(installed)
	if err != nil || string(got) != original {
		t.Fatalf("the installed binary is %q, %v; a failed install replaced it", got, err)
	}
	if stray, _ := filepath.Glob(filepath.Join(binDir, ".mtg-*")); len(stray) != 0 {
		t.Fatalf("a failed install left %v staged beside the binary", stray)
	}
}

// A listed, verified release lands, is asked for its version before it is the
// installed one, and nothing is restarted. Installed then names the new one.
func TestInstallLandsAVerifiedReleaseWithoutRestarting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the staged binary is run for its version, which needs a POSIX host")
	}
	binDir := t.TempDir()
	t.Setenv("PUI_BIN_FOLDER", binDir)
	installed := engine.GetBinaryPath()
	if err := os.WriteFile(installed, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload := "#!/bin/sh\necho \"9.9.9 (go1.26 fake)\"\n"
	archive, sum := mtgTarball(t, "mtg-proxy-9.9.9-linux-amd64", payload)
	c := channel(t, "v9.9.9", archive, sum)
	if err := c.Install(context.Background(), "v9.9.9"); err != nil {
		t.Fatalf("Install: %v", err)
	}
	got, err := os.ReadFile(installed)
	if err != nil || string(got) != payload {
		t.Fatalf("installed binary = %q, %v; want the release's member", got, err)
	}
	if v, err := c.Installed(context.Background()); err != nil || v != "9.9.9" {
		t.Fatalf("Installed = %q, %v; want the version the new binary reports", v, err)
	}

	// A member that is not a program is caught by the version probe, and the
	// binary that was there stays.
	broken, brokenSum := mtgTarball(t, "mtg-proxy-9.9.9-linux-amd64", "this is not an executable\n")
	c = channel(t, "v9.9.9", broken, brokenSum)
	if err := c.Install(context.Background(), "v9.9.9"); err == nil || !strings.Contains(err.Error(), "does not run here") {
		t.Fatalf("Install of a non-program = %v; want the version probe's refusal", err)
	}
	if got, _ := os.ReadFile(installed); string(got) != payload {
		t.Fatalf("a refused install replaced the binary with %q", got)
	}
}

// The real channel, where an operator says so (MTG_INSTALL_E2E=1): the asset
// name, the checksums file and the tarball layout are mtg-proxy's, not a fake's.
func TestInstallFromTheRealChannel(t *testing.T) {
	if os.Getenv("MTG_INSTALL_E2E") != "1" {
		t.Skip("MTG_INSTALL_E2E not set")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("the published asset this fetches is linux/amd64")
	}
	t.Setenv("PUI_BIN_FOLDER", t.TempDir())
	c := New(Deps{Platform: "amd64"})
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	versions, err := c.Available(ctx)
	if err != nil || len(versions) == 0 {
		// A 404 here is GitHub hiding a private repository: the panel downloads
		// anonymously from a customer's box, so a private channel installs nothing.
		t.Fatalf("Available = %v, %v", versions, err)
	}
	if err := c.Install(ctx, versions[0]); err != nil {
		t.Fatalf("Install(%s): %v", versions[0], err)
	}
	got, err := c.Installed(ctx)
	if err != nil || "v"+got != versions[0] {
		t.Fatalf("Installed = %q, %v; want the %s just installed", got, err, versions[0])
	}
}

// A release the panel refuses at boot is neither offered nor installed.
// The installed binary survives even when the refused payload's archive checksum is correct.
func TestTheInstallerRefusesAReleaseThePanelRefusesAtBoot(t *testing.T) {
	binDir := t.TempDir()
	t.Setenv("PUI_BIN_FOLDER", binDir)
	c := channel(t, "v9.9.9", nil, "", "v1.3.0", "v1.8.0")
	got, err := c.Available(context.Background())
	if err != nil || strings.Join(got, ",") != "v9.9.9" {
		t.Fatalf("Available = %v, %v; want the releases older than %s dropped", got, err, engine.MinimumVersion)
	}
	if err := c.Install(context.Background(), "v1.3.0"); !errors.Is(err, core.ErrUnknownVersion) {
		t.Fatalf("Install(v1.3.0) = %v, want ErrUnknownVersion", err)
	}
	if runtime.GOOS == "windows" {
		return
	}
	installed := engine.GetBinaryPath()
	if err := os.WriteFile(installed, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	archive, sum := mtgTarball(t, "mtg-proxy-9.9.9-linux-amd64", "#!/bin/sh\necho \"1.3.0 (go1.26 fake)\"\n")
	c = channel(t, "v9.9.9", archive, sum)
	err = c.Install(context.Background(), "v9.9.9")
	if err == nil || !strings.Contains(err.Error(), "refuses at boot") || !strings.Contains(err.Error(), "older than "+engine.MinimumVersion) {
		t.Fatalf("Install of a binary reporting 1.3.0 = %v; want the boot refusal named", err)
	}
	if got, _ := os.ReadFile(installed); string(got) != "old" {
		t.Fatalf("a refused install replaced the binary with %q", got)
	}
}
