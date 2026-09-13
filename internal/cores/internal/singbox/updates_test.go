package singbox

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
)

func updateArchive(t *testing.T, version, program string) ([]byte, string) {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tarball := tar.NewWriter(gz)
	name := "sing-box-" + strings.TrimPrefix(version, "v") + "-linux-amd64/sing-box"
	if err := tarball.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(program)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarball.Write([]byte(program)); err != nil {
		t.Fatal(err)
	}
	if err := tarball.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(buffer.Bytes())
	return buffer.Bytes(), hex.EncodeToString(digest[:])
}

func updateChannel(t *testing.T, version string, archive []byte, digest string) *Core {
	t.Helper()
	bare := strings.TrimPrefix(version, "v")
	asset := "sing-box-" + bare + "-linux-amd64.tar.gz"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/releases":
			_, _ = fmt.Fprintf(w, `[{"tag_name":%q,"assets":[{"name":%q},{"name":%q}]}]`, "singbox-"+version, asset, asset+".sha256")
		case strings.HasSuffix(r.URL.Path, ".sha256"):
			_, _ = fmt.Fprintf(w, "%s  %s\n", digest, asset)
		case strings.HasSuffix(r.URL.Path, ".tar.gz"):
			_, _ = w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	previousList, previousBase := releasesURL, downloadBase
	releasesURL, downloadBase = server.URL+"/releases", server.URL
	t.Cleanup(func() { releasesURL, downloadBase = previousList, previousBase })
	return New(Deps{Platform: "amd64", Binary: filepath.Join(t.TempDir(), "sing-box"), HTTPClient: func(time.Duration) *http.Client { return server.Client() }})
}

func candidateScript(version, tags, check string) string {
	return fmt.Sprintf("#!/bin/sh\nif [ \"$1\" = version ]; then\n echo 'sing-box version %s'\n echo 'Tags: %s'\n exit 0\nfi\n%s\n", version, tags, check)
}

func TestSingboxAvailableOffersCompleteCompatibleBuilds(t *testing.T) {
	releases := []map[string]any{}
	for _, tag := range []string{"singbox-v1.14.0", "singbox-v1.14.2", "singbox-v1.14.1", "singbox-v1.15.0", "singbox-v1.13.9", "v1.14.3", "singbox-v1.14.4-rc.1"} {
		asset := "sing-box-" + strings.TrimPrefix(strings.TrimPrefix(tag, "singbox-"), "v") + "-linux-amd64.tar.gz"
		releases = append(releases, map[string]any{"tag_name": tag, "assets": []map[string]string{{"name": asset}, {"name": asset + ".sha256"}}})
	}
	releases[2]["draft"] = true
	releases = append(releases, map[string]any{"tag_name": "singbox-v1.14.5", "assets": []map[string]string{{"name": "sing-box-1.14.5-linux-amd64.tar.gz"}}})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _ = json.NewEncoder(w).Encode(releases) }))
	defer server.Close()
	previous := releasesURL
	releasesURL = server.URL
	defer func() { releasesURL = previous }()
	c := New(Deps{Platform: "amd64", HTTPClient: func(time.Duration) *http.Client { return server.Client() }})
	got, err := c.Available(context.Background())
	if err != nil || strings.Join(got, ",") != "v1.14.2,v1.14.0" {
		t.Fatalf("Available = %v, %v; want only complete, stable, compatible builds in descending order", got, err)
	}
	if err := c.Install(context.Background(), "v1.15.0"); !errors.Is(err, core.ErrUnknownVersion) {
		t.Fatalf("unsupported install = %v, want ErrUnknownVersion", err)
	}
	if core.Bind(c).Versions == nil {
		t.Fatal("sing-box must expose working version management")
	}
}

func TestSingboxFailedUpdatePreservesInstalledBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("candidate executables in this test use POSIX shell")
	}
	for _, scenario := range []string{"checksum", "wrong-version", "missing-statistics", "protocol-config", "active-config"} {
		t.Run(scenario, func(t *testing.T) {
			version, tags, check := "1.14.1", RequiredBuildTags, "exit 0"
			switch scenario {
			case "wrong-version":
				version = "1.14.2"
			case "missing-statistics":
				tags = strings.ReplaceAll(tags, "with_v2ray_api,", "")
			case "protocol-config":
				check = "echo 'unsupported api service' >&2; exit 1"
			case "active-config":
				check = "case \"$3\" in */active.json) echo 'active config refused' >&2; exit 1 ;; esac\nexit 0"
			}
			archive, digest := updateArchive(t, "v1.14.1", candidateScript(version, tags, check))
			if scenario == "checksum" {
				digest = strings.Repeat("0", 64)
			}
			c := updateChannel(t, "v1.14.1", archive, digest)
			if err := os.WriteFile(c.binaryPath(), []byte("previous-good-binary"), 0o755); err != nil {
				t.Fatal(err)
			}
			if scenario == "active-config" {
				c.instances[7] = &instance{inst: core.Instance{ID: 7}, cfgPath: filepath.Join(filepath.Dir(c.binaryPath()), "active.json")}
			}
			err := c.Install(context.Background(), "v1.14.1")
			if err == nil || !strings.Contains(err.Error(), "preserved") {
				t.Fatalf("Install = %v, want explicit refusal preserving the installed executable", err)
			}
			data, readErr := os.ReadFile(c.binaryPath())
			if readErr != nil || string(data) != "previous-good-binary" {
				t.Fatalf("installed = %q, %v; old binary must survive every failed preflight", data, readErr)
			}
			staged, _ := filepath.Glob(filepath.Join(filepath.Dir(c.binaryPath()), ".singbox-update-*"))
			if len(staged) != 0 {
				t.Fatalf("staged files leaked: %v", staged)
			}
		})
	}
}

func TestSingboxUpdateInstallsVerifiedBuildWithoutStartingIt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("candidate executable uses POSIX shell")
	}
	program := candidateScript("1.14.1", RequiredBuildTags, "[ \"$1\" = check ] || exit 91\nexit 0")
	archive, digest := updateArchive(t, "v1.14.1", program)
	c := updateChannel(t, "v1.14.1", archive, digest)
	if err := c.Install(context.Background(), "v1.14.1"); err != nil {
		t.Fatal(err)
	}
	got, err := c.Installed(context.Background())
	if err != nil || got != "1.14.1" || len(c.instances) != 0 {
		t.Fatalf("Installed = %q, %v; instances=%d; want verified disk version without a daemon start", got, err, len(c.instances))
	}
	data, err := os.ReadFile(c.binaryPath())
	if err != nil || string(data) != program {
		t.Fatalf("installed bytes differ from the verified release: %v", err)
	}
}

func TestSingboxUpdateRefusesIncompleteMetadataAndConcurrentInstall(t *testing.T) {
	c := updateChannel(t, "v1.14.1", nil, strings.Repeat("g", 64))
	if err := c.Install(context.Background(), "v1.14.1"); err == nil || !strings.Contains(err.Error(), "malformed release checksum") {
		t.Fatalf("Install = %v, want checksum refusal before archive download", err)
	}
	c.updateMu.Lock()
	if err := c.Install(context.Background(), "v1.14.1"); err == nil || !strings.Contains(err.Error(), "already in progress") {
		t.Fatalf("concurrent Install = %v, want busy refusal", err)
	}
	c.updateMu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Install(ctx, "v1.14.1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled Install = %v, want context.Canceled", err)
	}
}

func TestPublishedUpdateCandidate(t *testing.T) {
	binary, version := os.Getenv("SINGBOX_UPDATE_E2E_BINARY"), os.Getenv("SINGBOX_UPDATE_E2E_VERSION")
	if binary == "" || version == "" {
		t.Skip("set SINGBOX_UPDATE_E2E_BINARY and SINGBOX_UPDATE_E2E_VERSION to validate a release build")
	}
	if err := validateBuild(context.Background(), binary, version); err != nil {
		t.Fatal(err)
	}
	if err := validateUpdateProtocols(context.Background(), binary); err != nil {
		t.Fatal(err)
	}
}

func TestPublishedUpdateInstallation(t *testing.T) {
	path, version := os.Getenv("SINGBOX_UPDATE_E2E_ARCHIVE"), os.Getenv("SINGBOX_UPDATE_E2E_VERSION")
	if path == "" || version == "" {
		t.Skip("set SINGBOX_UPDATE_E2E_ARCHIVE and SINGBOX_UPDATE_E2E_VERSION for a real installation")
	}
	archive, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(archive)
	c := updateChannel(t, version, archive, hex.EncodeToString(digest[:]))
	if err := c.Install(context.Background(), version); err != nil {
		t.Fatal(err)
	}
	installed, err := c.Installed(context.Background())
	if err != nil || installed != strings.TrimPrefix(version, "v") {
		t.Fatalf("Installed = %q, %v; want the selected release on disk", installed, err)
	}
	t.Setenv("SINGBOX_E2E_BINARY", c.binaryPath())
	t.Run("traffic-and-sessions-after-install", TestRealTrafficIsBilledAndSeen)
	t.Run("active-runtime-applies-only-after-restart", func(t *testing.T) {
		cert, key := selfSigned(t, t.TempDir())
		c.deps.StateDir = t.TempDir()
		inst := core.Instance{
			ID: 8, Kind: KindTUIC, Tag: "update-live", Enable: true, Listen: "127.0.0.1", Port: freePort(t),
			Settings: settingsJSON(t, cert, key), Users: []core.User{e2eUser(1)},
		}
		t.Cleanup(func() { _ = c.StopAll(context.Background()) })
		if err := c.Reconcile(context.Background(), []core.Instance{inst}); err != nil {
			t.Fatal(err)
		}
		before, err := c.Health(context.Background())
		if err != nil || len(before.Instances) != 1 || !before.Instances[0].Ready {
			t.Fatalf("before update: %+v, %v; want a ready managed process", before, err)
		}
		pid := before.Instances[0].PID
		if err := c.Install(context.Background(), version); err != nil {
			t.Fatal(err)
		}
		after, err := c.Health(context.Background())
		if err != nil || len(after.Instances) != 1 || after.Instances[0].PID != pid || !after.Instances[0].Ready {
			t.Fatalf("after install: %+v, %v; want the same ready process", after, err)
		}
		exe, err := os.Readlink("/proc/" + strconv.Itoa(pid) + "/exe")
		if err != nil || !strings.HasSuffix(exe, " (deleted)") {
			t.Fatalf("loaded executable = %q, %v; want a pending on-disk replacement", exe, err)
		}
		if err := c.StopAll(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := c.Reconcile(context.Background(), []core.Instance{inst}); err != nil {
			t.Fatal(err)
		}
		restarted, err := c.Health(context.Background())
		if err != nil || len(restarted.Instances) != 1 || restarted.Instances[0].PID == pid || !restarted.Instances[0].Ready {
			t.Fatalf("after restart: %+v, %v; want a new ready process", restarted, err)
		}
	})
}
