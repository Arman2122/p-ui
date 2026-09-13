package mtproto

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
	engine "github.com/Arman2122/p-ui/internal/mtproto"
)

// Stage a checksummed release for the build's explicit platform, then verify its identity.
// Atomic replacement leaves running sidecars on their old inode until an operator restarts them.
var (
	releasesURL      = "https://api.github.com/repos/Arman2122/mtg-proxy/releases"
	downloadBase     = "https://github.com/Arman2122/mtg-proxy/releases/download"
	stableReleaseTag = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+$`)
)

const (
	maxListBytes    = 4 << 20
	maxSumsBytes    = 64 << 10
	maxArchiveBytes = 64 << 20
	maxBinaryBytes  = 64 << 20
)

// Installed is what the binary on disk says of itself: "1.3.0" from
// "1.3.0 (go1.26.6: …)". A binary that is not there is ErrNotInstalled.
func (c *Core) Installed(ctx context.Context) (string, error) {
	if _, err := os.Stat(engine.GetBinaryPath()); err != nil {
		return "", core.ErrNotInstalled
	}
	return engine.BinaryVersion(ctx, engine.GetBinaryPath())
}

// Available lists the channel's release tags, newest first, pre-releases left out.
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
		return nil, fmt.Errorf("mtproto: GitHub API returned %s", resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxListBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxListBytes {
		return nil, fmt.Errorf("mtproto: release list exceeds %d bytes", maxListBytes)
	}
	var releases []struct {
		TagName    string `json:"tag_name"`
		Prerelease bool   `json:"prerelease"`
		Draft      bool   `json:"draft"`
	}
	if err := json.Unmarshal(body, &releases); err != nil {
		return nil, err
	}
	tags := make([]string, 0, len(releases))
	for _, r := range releases {
		// A release the panel refuses at boot is not offered: installing it
		// would take every MTProto inbound offline at the next restart.
		if !r.Draft && !r.Prerelease && stableReleaseTag.MatchString(r.TagName) && engine.CheckVersion(r.TagName) == nil {
			tags = append(tags, r.TagName)
		}
	}
	return tags, nil
}

// Install fetches, verifies, stages and lands one listed release once the
// candidate has run `--check` over the panel's config and every live one.
func (c *Core) Install(ctx context.Context, version string) error {
	if !c.updateMu.TryLock() {
		return errors.New("mtproto: an installation is already in progress")
	}
	defer c.updateMu.Unlock()
	versions, err := c.Available(ctx)
	if err != nil {
		return err
	}
	if !slices.Contains(versions, version) {
		return fmt.Errorf("%w: %q", core.ErrUnknownVersion, version)
	}
	if c.deps.Platform == "" {
		return errors.New("mtproto: this build does not know its platform, so it cannot name the mtg-proxy asset")
	}
	bare := strings.TrimPrefix(version, "v")
	pkg := fmt.Sprintf("mtg-proxy-%s-linux-%s", bare, c.deps.Platform)
	client := c.httpClient(60 * time.Second)
	want, err := publishedSum(ctx, client, fmt.Sprintf("%s/%s/mtg-proxy-%s-checksums.txt", downloadBase, version, bare), pkg+".tar.gz")
	if err != nil {
		return err
	}
	archive, err := download(ctx, client, fmt.Sprintf("%s/%s/%s.tar.gz", downloadBase, version, pkg), want)
	if err != nil {
		return err
	}
	defer os.Remove(archive)
	staged, err := stageFromTarGz(archive, pkg+"/mtg-proxy", engine.GetBinaryPath())
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(staged) }()
	reported, err := engine.BinaryVersion(ctx, staged)
	if err != nil {
		return fmt.Errorf("mtproto: the downloaded %s does not run here, so it was not installed: %w", pkg, err)
	}
	if err := engine.CheckVersion(reported); err != nil {
		return fmt.Errorf("mtproto: the downloaded %s reports a version the panel refuses at boot, so it was not installed: %w", pkg, err)
	}
	if strings.TrimPrefix(reported, "v") != bare {
		return fmt.Errorf("mtproto: downloaded %s reports %q, which does not match requested %s; it was not installed", pkg, reported, version)
	}
	if err := validateUpdateConfig(ctx, staged); err != nil {
		return err
	}
	if err := validateRunningConfigs(ctx, staged, c.mgr.RunningConfigs()); err != nil {
		return err
	}
	return replaceStagedBinary(ctx, staged, engine.GetBinaryPath())
}

func replaceStagedBinary(ctx context.Context, staged, dest string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(staged, dest)
}

func (c *Core) httpClient(timeout time.Duration) *http.Client {
	if c.deps.HTTPClient != nil {
		return c.deps.HTTPClient(timeout)
	}
	return &http.Client{Timeout: timeout}
}

// publishedSum reads the checksums file and returns the sha256 it lists for name.
func publishedSum(ctx context.Context, client *http.Client, url, name string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("mtproto: download checksums: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("mtproto: download checksums: unexpected HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxSumsBytes))
	if err != nil {
		return "", err
	}
	return parseChecksum(raw, name)
}

// parseChecksum finds name in "<hex>  <name>" lines and returns its lowercase hex.
func parseChecksum(sums []byte, name string) (string, error) {
	for line := range strings.SplitSeq(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == name {
			decoded, err := hex.DecodeString(fields[0])
			if err != nil || len(decoded) != sha256.Size {
				return "", fmt.Errorf("mtproto: checksums: malformed entry for %s", name)
			}
			return strings.ToLower(fields[0]), nil
		}
	}
	return "", fmt.Errorf("mtproto: checksums: no entry for %s", name)
}

// download fetches url to a temp file and refuses it unless its sha256 is want.
func download(ctx context.Context, client *http.Client, url, want string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("mtproto: download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("mtproto: download: unexpected HTTP %d", resp.StatusCode)
	}
	file, err := os.CreateTemp("", "mtg-*.tar.gz")
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
		return "", fmt.Errorf("mtproto: download: archive exceeds %d bytes", maxArchiveBytes)
	}
	if got := hex.EncodeToString(hasher.Sum(nil)); got != want {
		return "", fmt.Errorf("mtproto: the downloaded archive does not match the published checksum (expected %s, got %s); it was not installed", want, got)
	}
	ok = true
	return path, nil
}

// stageFromTarGz extracts one member to a temp file beside dest, executable;
// the caller renames it into place.
func stageFromTarGz(archive, member, dest string) (string, error) {
	f, err := os.Open(archive)
	if err != nil {
		return "", err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return "", fmt.Errorf("mtproto: %s is not in the archive", member)
		}
		if err != nil {
			return "", err
		}
		if hdr.Typeflag != tar.TypeReg || filepath.ToSlash(hdr.Name) != member {
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return "", err
		}
		tmp, err := os.CreateTemp(filepath.Dir(dest), ".mtg-*")
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
		n, err := io.Copy(tmp, io.LimitReader(tr, maxBinaryBytes+1))
		if err != nil {
			return "", err
		}
		if n > maxBinaryBytes {
			return "", fmt.Errorf("mtproto: binary exceeds %d bytes", maxBinaryBytes)
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
}
