package xray

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	"github.com/Arman2122/p-ui/internal/logger"
	engine "github.com/Arman2122/p-ui/internal/xray"
)

/*
The release channel is XTLS/Xray-core on GitHub, verified through the .dgst
sidecar XTLS publishes beside each asset: TLS protects the transport, the digest
the artifact. Both URLs are vars so a test can point them at an httptest server.

Install stages the new binary beside the installed one and, when a process is
running, asks the NEW binary whether it can load the config this box runs before
anything is replaced (ledger L2): a download that verifies but cannot load must
never become the installed binary, because every later restart would refuse
too and nothing would put the old one back. It does not restart: that is
Supervisor's, so an upgrade can be staged and applied when traffic allows.
*/
var (
	releasesURL  = "https://api.github.com/repos/XTLS/Xray-core/releases"
	downloadBase = "https://github.com/XTLS/Xray-core/releases/download"
)

const (
	// maxReleasesBytes caps the GitHub release-list read; a page is ~100 KiB. F285.
	maxReleasesBytes = 4 << 20
	// maxDigestBytes caps the .dgst sidecar read; it is a few hundred bytes.
	maxDigestBytes  = 64 << 10
	maxArchiveBytes = 200 << 20
	maxBinaryBytes  = 200 << 20
	// oldestOffered is the first release the panel's config generator targets;
	// an older core cannot load what the panel writes.
	oldestMajor, oldestMinor, oldestPatch = 26, 6, 27
)

type release struct {
	TagName string `json:"tag_name"`
}

// Installed is the running process's version, else what the binary on disk
// says of itself; a binary that is not there is ErrNotInstalled.
func (c *Core) Installed(ctx context.Context) (string, error) {
	if p := c.mgr.Current(); p != nil && p.IsRunning() {
		if v := p.GetXrayVersion(); v != "" && v != "Unknown" {
			return v, nil
		}
	}
	if _, err := os.Stat(engine.GetBinaryPath()); err != nil {
		return "", core.ErrNotInstalled
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, engine.GetBinaryPath(), "-version").Output()
	if err != nil {
		return "", fmt.Errorf("xray: version: %w", err)
	}
	// "Xray 26.7.28 (Xray, Penetrates Everything.) 5ca6f4b (go1.26.5 linux/amd64)"
	fields := strings.Fields(string(out))
	if len(fields) < 2 {
		return "", fmt.Errorf("xray: version: unexpected answer %q", strings.TrimSpace(string(out)))
	}
	return fields[1], nil
}

// Available lists the tags the panel's config generator can drive, newest first
// as GitHub returns them.
func (c *Core) Available(ctx context.Context) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient(10 * time.Second).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxDigestBytes))
		var apiErr struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(body, &apiErr) == nil && apiErr.Message != "" {
			return nil, fmt.Errorf("xray: GitHub API: %s", apiErr.Message)
		}
		return nil, fmt.Errorf("xray: GitHub API returned %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxReleasesBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxReleasesBytes {
		return nil, fmt.Errorf("xray: release list exceeds %d bytes", maxReleasesBytes)
	}
	var releases []release
	if err := json.Unmarshal(body, &releases); err != nil {
		return nil, err
	}
	versions := make([]string, 0, len(releases))
	for _, r := range releases {
		if loadable(r.TagName) {
			versions = append(versions, r.TagName)
		}
	}
	return versions, nil
}

// loadable is the >= oldestOffered rule on a "vMAJOR.MINOR.PATCH" tag.
func loadable(tag string) bool {
	parts := strings.Split(strings.TrimPrefix(tag, "v"), ".")
	if len(parts) != 3 {
		return false
	}
	n := make([]int, 3)
	for i, p := range parts {
		v, err := strconv.Atoi(p)
		if err != nil {
			return false
		}
		n[i] = v
	}
	switch {
	case n[0] != oldestMajor:
		return n[0] > oldestMajor
	case n[1] != oldestMinor:
		return n[1] > oldestMinor
	default:
		return n[2] >= oldestPatch
	}
}

// Install fetches, verifies, stages and lands one listed release. See the
// package note for why it checks the config first and restarts nothing.
func (c *Core) Install(ctx context.Context, version string) error {
	versions, err := c.Available(ctx)
	if err != nil {
		return err
	}
	if !slices.Contains(versions, version) {
		return fmt.Errorf("%w: %q", core.ErrUnknownVersion, version)
	}
	archive, err := c.download(ctx, version)
	if err != nil {
		return err
	}
	defer os.Remove(archive)
	staged, err := stageFromZip(archive, "xray", engine.GetBinaryPath())
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(staged) }()
	if p := c.mgr.Current(); p != nil && p.GetConfig() != nil {
		if err := engine.TestConfigWith(staged, p.GetConfig()); err != nil {
			return fmt.Errorf("xray %s cannot load this panel's config, so it was not installed: %w", version, err)
		}
	} else {
		logger.Warning("xray update pre-flight skipped: no running config to test against")
	}
	// Renaming over a running executable is fine on Linux: the old process
	// keeps its inode until it exits.
	return os.Rename(staged, engine.GetBinaryPath())
}

func (c *Core) httpClient(timeout time.Duration) *http.Client {
	if c.deps.HTTPClient != nil {
		return c.deps.HTTPClient(timeout)
	}
	return &http.Client{Timeout: timeout}
}

// download fetches the archive for this platform to a temp file and verifies
// it against the published SHA2-256 before handing the path back.
func (c *Core) download(ctx context.Context, version string) (string, error) {
	name := fmt.Sprintf("Xray-linux-%s.zip", engine.ReleaseAssetArch())
	url := fmt.Sprintf("%s/%s/%s", downloadBase, version, name)
	client := c.httpClient(60 * time.Second)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download xray: unexpected HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxArchiveBytes {
		return "", fmt.Errorf("download xray: archive exceeds %d bytes", maxArchiveBytes)
	}
	file, err := os.CreateTemp("", "xray-*.zip")
	if err != nil {
		return "", err
	}
	path := file.Name()
	ok := false
	defer func() {
		_ = file.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	hasher := sha256.New()
	n, err := io.Copy(io.MultiWriter(file, hasher), io.LimitReader(resp.Body, maxArchiveBytes+1))
	if err != nil {
		return "", err
	}
	if n > maxArchiveBytes {
		return "", fmt.Errorf("download xray: archive exceeds %d bytes", maxArchiveBytes)
	}
	want, err := fetchDigest(ctx, client, url+".dgst")
	if err != nil {
		return "", err
	}
	if got := hex.EncodeToString(hasher.Sum(nil)); !strings.EqualFold(got, want) {
		return "", fmt.Errorf("Xray update aborted: the downloaded archive does not match the official SHA-256 checksum, so the image is corrupted or differs from the official release. Please exit and re-download the official image, then try again (expected %s, got %s)", want, got)
	}
	ok = true
	return path, nil
}

// fetchDigest reads the .dgst sidecar XTLS publishes next to each asset.
func fetchDigest(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("download xray checksum: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download xray checksum: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download xray checksum: unexpected HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxDigestBytes))
	if err != nil {
		return "", fmt.Errorf("download xray checksum: %w", err)
	}
	return parseDigestSHA256(raw)
}

// parseDigestSHA256 extracts the lowercase SHA2-256 hex from an XTLS .dgst
// file, whose lines are "ALGO= <hex>" (the relevant one being "SHA2-256= ...").
func parseDigestSHA256(dgst []byte) (string, error) {
	for line := range strings.SplitSeq(string(dgst), "\n") {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), "SHA2-256=")
		if !ok {
			continue
		}
		h := strings.ToLower(strings.TrimSpace(rest))
		if len(h) != 64 {
			return "", errors.New("xray checksum: malformed SHA2-256 entry in digest")
		}
		return h, nil
	}
	return "", errors.New("xray checksum: no SHA2-256 entry in digest")
}

// stageFromZip extracts one member of the archive to a temp file beside its
// destination, executable, and hands the path back; the caller renames it.
func stageFromZip(archive, member, dest string) (string, error) {
	zr, err := zip.OpenReader(archive)
	if err != nil {
		return "", err
	}
	defer zr.Close()
	f, err := zr.Open(member)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".xray-*")
	if err != nil {
		return "", err
	}
	path := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(path)
		}
	}()
	n, err := io.Copy(tmp, io.LimitReader(f, maxBinaryBytes+1))
	if err != nil {
		return "", err
	}
	if n > maxBinaryBytes {
		return "", fmt.Errorf("xray binary exceeds %d bytes", maxBinaryBytes)
	}
	if err := tmp.Chmod(0o755); err != nil {
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	ok = true
	return path, nil
}
