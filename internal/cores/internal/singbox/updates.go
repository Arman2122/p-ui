package singbox

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
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Arman2122/p-ui/internal/core"
)

// Panel builds retain the statistics API omitted from upstream's default binaries.
var (
	releasesURL  = "https://api.github.com/repos/Arman2122/p-ui/releases?per_page=100"
	downloadBase = "https://github.com/Arman2122/p-ui/releases/download"
)

const (
	maxReleaseBytes = 4 << 20
	maxDigestBytes  = 64 << 10
	maxArchiveBytes = 100 << 20
	maxBinaryBytes  = 200 << 20
)

const RequiredBuildTags = "with_v2ray_api,with_quic,with_utls,with_clash_api,with_acme"

type coreRelease struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name string `json:"name"`
	} `json:"assets"`
}

func stableParts(version string) ([]int, bool) {
	parts := strings.Split(strings.TrimPrefix(version, "v"), ".")
	if len(parts) != 3 {
		return nil, false
	}
	values := make([]int, 3)
	for i, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 || strconv.Itoa(n) != part {
			return nil, false
		}
		values[i] = n
	}
	return values, true
}

// New minor lines require an adapter review; patch updates still validate real configs.
func compatibleRelease(version string) bool {
	parts, valid := stableParts(version)
	baseline, baselineValid := stableParts(Version)
	return valid && baselineValid && parts[0] == baseline[0] && parts[1] == baseline[1] && parts[2] >= baseline[2]
}

func (c *Core) packageName(version string) (string, error) {
	if !slices.Contains([]string{"amd64", "arm64", "armv7", "armv6", "armv5", "386", "s390x"}, c.deps.Platform) {
		return "", errors.New("singbox: this panel build has no supported release platform")
	}
	return "sing-box-" + strings.TrimPrefix(version, "v") + "-linux-" + c.deps.Platform, nil
}

func (c *Core) httpClient(timeout time.Duration) *http.Client {
	if c.deps.HTTPClient != nil {
		return c.deps.HTTPClient(timeout)
	}
	return &http.Client{Timeout: timeout}
}

func (c *Core) Available(ctx context.Context) ([]string, error) {
	if _, err := c.packageName(Version); err != nil {
		return nil, err
	}
	body, err := readReleaseFile(ctx, c.httpClient(15*time.Second), releasesURL, maxReleaseBytes)
	if err != nil {
		return nil, fmt.Errorf("singbox: compatible release channel: %w", err)
	}
	var releases []coreRelease
	if err := json.Unmarshal(body, &releases); err != nil {
		return nil, fmt.Errorf("singbox: release list: %w", err)
	}
	versions := []string{}
	for _, release := range releases {
		if release.Draft || release.Prerelease || !strings.HasPrefix(release.TagName, "singbox-v") {
			continue
		}
		version := strings.TrimPrefix(release.TagName, "singbox-")
		if !compatibleRelease(version) {
			continue
		}
		pkg, _ := c.packageName(version)
		archive, checksum := false, false
		for _, asset := range release.Assets {
			archive = archive || asset.Name == pkg+".tar.gz"
			checksum = checksum || asset.Name == pkg+".tar.gz.sha256"
		}
		if archive && checksum && !slices.Contains(versions, version) {
			versions = append(versions, version)
		}
	}
	sort.Slice(versions, func(i, j int) bool {
		a, _ := stableParts(versions[i])
		b, _ := stableParts(versions[j])
		return a[2] > b[2]
	})
	return versions, nil
}

func (c *Core) Install(ctx context.Context, version string) error {
	if !c.updateMu.TryLock() {
		return errors.New("singbox: an installation is already in progress")
	}
	defer c.updateMu.Unlock()
	versions, err := c.Available(ctx)
	if err != nil {
		return err
	}
	if !slices.Contains(versions, version) {
		return fmt.Errorf("%w: %q", core.ErrUnknownVersion, version)
	}
	pkg, err := c.packageName(version)
	if err != nil {
		return err
	}
	base := downloadBase + "/singbox-" + version + "/" + pkg + ".tar.gz"
	client := c.httpClient(90 * time.Second)
	digestFile, err := readReleaseFile(ctx, client, base+".sha256", maxDigestBytes)
	if err != nil {
		return err
	}
	want, err := releaseDigest(digestFile, pkg+".tar.gz")
	if err != nil {
		return err
	}
	archive, err := downloadRelease(ctx, client, base, want)
	if err != nil {
		return err
	}
	defer os.Remove(archive)
	staged, err := stageRelease(archive, pkg+"/sing-box", c.binaryPath())
	if err != nil {
		return err
	}
	defer os.Remove(staged)
	if err := validateBuild(ctx, staged, version); err != nil {
		return err
	}
	if err := validateUpdateProtocols(ctx, staged); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, instance := range c.instances {
		if err := checkConfig(ctx, staged, instance.cfgPath); err != nil {
			return fmt.Errorf("singbox: update cannot load inbound %d; installed binary preserved: %w", instance.inst.ID, err)
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return os.Rename(staged, c.binaryPath())
}

func validateBuild(ctx context.Context, binary, requested string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, binary, "version").Output()
	if err != nil {
		return fmt.Errorf("singbox: candidate does not run on this host; installed binary preserved: %w", err)
	}
	version, err := parseVersion(string(out))
	if err != nil || strings.TrimPrefix(version, "v") != strings.TrimPrefix(requested, "v") {
		return fmt.Errorf("singbox: candidate reports %q, requested %q; installed binary preserved", version, requested)
	}
	tags := []string{}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "Tags:") {
			tags = strings.Split(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "Tags:")), ",")
		}
	}
	for _, required := range strings.Split(RequiredBuildTags, ",") {
		if !slices.Contains(tags, required) {
			return fmt.Errorf("singbox: candidate lacks %s; installed binary preserved", required)
		}
	}
	return nil
}

func readReleaseFile(ctx context.Context, client *http.Client, url string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("singbox: release request returned HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("singbox: release response exceeds its size limit")
	}
	return data, nil
}

func releaseDigest(data []byte, asset string) (string, error) {
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != asset {
			continue
		}
		digest, err := hex.DecodeString(fields[0])
		if err != nil || len(digest) != sha256.Size {
			return "", errors.New("singbox: malformed release checksum")
		}
		return hex.EncodeToString(digest), nil
	}
	return "", errors.New("singbox: release checksum does not name the selected archive")
}

func downloadRelease(ctx context.Context, client *http.Client, url, want string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("singbox: archive download returned HTTP %d", response.StatusCode)
	}
	file, err := os.CreateTemp("", "singbox-update-*.tar.gz")
	if err != nil {
		return "", err
	}
	keep := false
	defer func() {
		_ = file.Close()
		if !keep {
			_ = os.Remove(file.Name())
		}
	}()
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, maxArchiveBytes+1))
	if err != nil {
		return "", err
	}
	if n > maxArchiveBytes || hex.EncodeToString(hash.Sum(nil)) != want {
		return "", errors.New("singbox: archive size or checksum verification failed; installed binary preserved")
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	keep = true
	return file.Name(), nil
}

func stageRelease(archive, member, destination string) (string, error) {
	file, err := os.Open(archive)
	if err != nil {
		return "", err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	reader := tar.NewReader(io.LimitReader(gz, maxBinaryBytes+(1<<20)))
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return "", errors.New("singbox: selected executable is absent from the archive")
		}
		if err != nil {
			return "", err
		}
		if header.Name != member || header.Typeflag != tar.TypeReg {
			continue
		}
		if header.Size <= 0 || header.Size > maxBinaryBytes {
			return "", errors.New("singbox: invalid archived binary size")
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return "", err
		}
		staged, err := os.CreateTemp(filepath.Dir(destination), ".singbox-update-*")
		if err != nil {
			return "", err
		}
		keep := false
		defer func() {
			_ = staged.Close()
			if !keep {
				_ = os.Remove(staged.Name())
			}
		}()
		n, err := io.Copy(staged, io.LimitReader(reader, maxBinaryBytes+1))
		if err != nil || n != header.Size {
			return "", errors.New("singbox: archived executable is truncated")
		}
		if err := staged.Chmod(0o755); err != nil {
			return "", err
		}
		if err := staged.Sync(); err != nil {
			return "", err
		}
		if err := staged.Close(); err != nil {
			return "", err
		}
		keep = true
		return staged.Name(), nil
	}
}
