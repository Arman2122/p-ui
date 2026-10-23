package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const maxGeofileDigestBytes = 64 << 10

// The updater's allowlisted publishers all provide .sha256sum files. Pin the
// release before fetching either artifact so a rolling latest cannot mix them.
func pinGeofileAssetURL(client *http.Client, latestURL string) (string, error) {
	original, err := url.Parse(latestURL)
	if err != nil {
		return "", err
	}
	repo, asset, ok := strings.Cut(original.Path, "/releases/latest/download/")
	if !ok || asset == "" || strings.Contains(asset, "/") {
		return "", fmt.Errorf("invalid geofile release URL %q", latestURL)
	}
	probe := *client
	probe.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, latestURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := probe.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	location, err := resp.Location()
	if err != nil || resp.StatusCode < 300 || resp.StatusCode >= 400 {
		return "", fmt.Errorf("geofile release did not redirect to an immutable tag (HTTP %d)", resp.StatusCode)
	}
	prefix := repo + "/releases/download/"
	tag, target, found := strings.Cut(strings.TrimPrefix(location.Path, prefix), "/")
	if location.Scheme != original.Scheme || location.Host != original.Host || location.User != nil ||
		!strings.HasPrefix(location.Path, prefix) || !found || tag == "" || tag == "." || tag == ".." ||
		target != asset || location.RawQuery != "" || location.Fragment != "" {
		return "", fmt.Errorf("unexpected geofile release redirect %q", location)
	}
	return location.String(), nil
}

func downloadGeofile(client *http.Client, latestURL, destPath string) (bool, error) {
	assetURL, err := pinGeofileAssetURL(client, latestURL)
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, assetURL, nil)
	if err != nil {
		return false, err
	}
	if info, err := os.Stat(destPath); err == nil && !info.ModTime().IsZero() {
		req.Header.Set("If-Modified-Since", info.ModTime().UTC().Format(http.TimeFormat))
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotModified {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("download geofile: unexpected HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxGeofileBytes {
		return false, fmt.Errorf("geofile exceeds %d bytes", maxGeofileBytes)
	}
	tmp, err := os.CreateTemp(filepath.Dir(destPath), ".geo-*")
	if err != nil {
		return false, err
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(tmp, hash), io.LimitReader(resp.Body, maxGeofileBytes+1))
	if err != nil {
		return false, err
	}
	if written > maxGeofileBytes {
		return false, fmt.Errorf("geofile exceeds %d bytes", maxGeofileBytes)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return false, err
	}
	if err := verifyGeofile(tmp); err != nil {
		return false, fmt.Errorf("refusing invalid geofile: %w", err)
	}
	u, _ := url.Parse(assetURL)
	want, err := fetchGeofileDigest(client, assetURL+".sha256sum", path.Base(u.Path))
	if err != nil {
		return false, err
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != want {
		return false, fmt.Errorf("geofile SHA-256 mismatch: expected %s, got %s", want, got)
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if modified, err := http.ParseTime(resp.Header.Get("Last-Modified")); err == nil {
		if err := os.Chtimes(tmp.Name(), modified, modified); err != nil {
			return false, err
		}
	}
	if err := os.Rename(tmp.Name(), destPath); err != nil {
		return false, err
	}
	return true, nil
}

func fetchGeofileDigest(client *http.Client, sumsURL, asset string) (string, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, sumsURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download geofile checksum: unexpected HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxGeofileDigestBytes+1))
	if err != nil {
		return "", err
	}
	if len(raw) > maxGeofileDigestBytes {
		return "", fmt.Errorf("geofile checksum exceeds %d bytes", maxGeofileDigestBytes)
	}
	for line := range strings.SplitSeq(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || path.Base(strings.TrimPrefix(fields[1], "*")) != asset {
			continue
		}
		decoded, err := hex.DecodeString(fields[0])
		if err != nil || len(decoded) != sha256.Size {
			return "", fmt.Errorf("invalid SHA-256 checksum for %s", asset)
		}
		return strings.ToLower(fields[0]), nil
	}
	return "", fmt.Errorf("no SHA-256 checksum for %s", asset)
}
