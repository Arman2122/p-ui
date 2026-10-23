package release

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/theupdateframework/go-tuf/v2/metadata"
	"github.com/theupdateframework/go-tuf/v2/metadata/config"
	"github.com/theupdateframework/go-tuf/v2/metadata/trustedmetadata"
	"github.com/theupdateframework/go-tuf/v2/metadata/updater"

	"github.com/Arman2122/p-ui/internal/node/privatefile"
)

type RepositoryConfig struct {
	Version     uint32 `json:"version"`
	MetadataURL string `json:"metadataURL"`
	TargetsURL  string `json:"targetsURL"`
	Platform    string `json:"platform"`
}

type Repository struct {
	dir           string
	configuration RepositoryConfig
	client        *http.Client
}

func validRepositoryURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.RawQuery == "" && u.Fragment == "" && !strings.Contains(u.Path, "..")
}

func InitializeRepository(dir string, cfg RepositoryConfig, root []byte) error {
	if !filepath.IsAbs(dir) || cfg.Version != 1 || !validPlatform(cfg.Platform) || !validRepositoryURL(cfg.MetadataURL) || !validRepositoryURL(cfg.TargetsURL) || len(root) > 512000 {
		return ErrRelease
	}
	if _, err := os.Lstat(dir); err == nil {
		return os.ErrExist
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	trusted, err := trustedmetadata.New(root)
	if err != nil {
		return err
	}
	if !trusted.Root.Signed.ConsistentSnapshot {
		return ErrRelease
	}
	temporary, err := os.MkdirTemp(filepath.Dir(dir), ".tuf-init-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(temporary)
	cache := filepath.Join(temporary, "metadata")
	if err := os.Mkdir(cache, 0o700); err != nil {
		return err
	}
	encoded, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := durableFile(filepath.Join(cache, "root.json"), root); err != nil {
		return err
	}
	if err := durableFile(filepath.Join(temporary, "repository.json"), encoded); err != nil {
		return err
	}
	return commitStage(temporary, dir)
}

func OpenRepository(dir string) (*Repository, error) {
	if !filepath.IsAbs(dir) {
		return nil, ErrRelease
	}
	if err := repositoryDirectory(dir); err != nil {
		return nil, err
	}
	if err := repositoryDirectory(filepath.Join(dir, "metadata")); err != nil {
		return nil, err
	}
	encoded, err := readPrivate(filepath.Join(dir, "repository.json"), 8192)
	if err != nil {
		return nil, err
	}
	var cfg RepositoryConfig
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&cfg) != nil || decoder.Decode(new(any)) != io.EOF || cfg.Version != 1 || !validPlatform(cfg.Platform) || !validRepositoryURL(cfg.MetadataURL) || !validRepositoryURL(cfg.TargetsURL) {
		return nil, ErrRelease
	}
	client := &http.Client{Timeout: 5 * time.Minute, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, ResponseHeaderTimeout: 15 * time.Second}, CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return errors.New("release repository redirects are refused")
	}}
	return &Repository{dir: dir, configuration: cfg, client: client}, nil
}

func (r *Repository) Fetch(ctx context.Context, sequence uint64, destination string) (result Manifest, returned error) {
	if sequence == 0 || !filepath.IsAbs(destination) {
		return result, ErrRelease
	}
	unlock, err := lockRepository(ctx, filepath.Join(r.dir, "lock"))
	if err != nil {
		return result, err
	}
	defer unlock()
	cache := filepath.Join(r.dir, "metadata")
	defer func() { returned = errors.Join(returned, syncMetadata(cache)) }()
	if err := syncMetadata(cache); err != nil {
		return result, err
	}
	root, err := readPrivate(filepath.Join(cache, "root.json"), 512000)
	if err != nil {
		return result, err
	}
	cfg, err := config.New(r.configuration.MetadataURL, root)
	if err != nil {
		return result, err
	}
	cfg.LocalMetadataDir, cfg.LocalTargetsDir = cache, filepath.Join(r.dir, "downloads")
	cfg.RemoteTargetsURL = r.configuration.TargetsURL
	cfg.MaxDelegations, cfg.MaxRootRotations = 16, 64
	cfg.TargetsMaxLength, cfg.SnapshotMaxLength = 1<<20, 1<<20
	cfg.Fetcher = &repositoryFetcher{ctx: ctx, client: r.client, base: r.configuration.MetadataURL}
	client, err := updater.New(cfg)
	if err != nil {
		return result, err
	}
	if err := client.Refresh(); err != nil {
		return result, err
	}
	if err := syncMetadata(cache); err != nil {
		return result, err
	}
	currentRoot, err := readPrivate(filepath.Join(cache, "root.json"), 512000)
	if err != nil {
		return result, err
	}
	trusted, err := trustedmetadata.New(currentRoot)
	if err != nil || !trusted.Root.Signed.ConsistentSnapshot {
		return result, ErrRelease
	}
	prefix := "nodes/" + r.configuration.Platform + "/" + strconv.FormatUint(sequence, 10) + "/"
	info, err := client.GetTargetInfo(prefix + "manifest.json")
	if err != nil {
		return result, err
	}
	if info == nil || info.Length <= 0 || info.Length > 64<<10 {
		return result, ErrRelease
	}
	data, err := r.targetBytes(ctx, info, 64<<10)
	if err != nil {
		return result, err
	}
	manifest, err := decodeManifest(data, r.configuration.Platform)
	if err != nil || manifest.Sequence != sequence {
		return result, ErrRelease
	}
	targets := make(map[string]*metadata.TargetFiles, len(manifest.Assets))
	for _, asset := range manifest.Assets {
		entry, err := client.GetTargetInfo(prefix + asset.Name)
		if err != nil {
			return result, err
		}
		if len(entry.Hashes) != 1 || entry.Length != asset.Size || hex.EncodeToString(entry.Hashes["sha256"]) != asset.SHA256 {
			return result, ErrRelease
		}
		targets[asset.Name] = entry
	}
	if err := syncMetadata(cache); err != nil {
		return result, err
	}
	temporary, err := os.MkdirTemp(filepath.Dir(destination), ".tuf-release-")
	if err != nil {
		return result, err
	}
	defer os.RemoveAll(temporary)
	for _, asset := range manifest.Assets {
		if err := r.downloadTarget(ctx, targets[asset.Name], filepath.Join(temporary, asset.Name)); err != nil {
			return result, err
		}
	}
	if err := durableFile(filepath.Join(temporary, "manifest.json"), data); err != nil {
		return result, err
	}
	if err := commitStage(temporary, destination); err != nil {
		return result, err
	}
	return manifest, nil
}

func (r *Repository) targetURL(info *metadata.TargetFiles) (string, error) {
	if info == nil || len(info.Hashes) != 1 || len(info.Hashes["sha256"]) != sha256.Size || info.Length <= 0 || info.Length > 512<<20 || !strings.HasPrefix(info.Path, "nodes/"+r.configuration.Platform+"/") || path.Clean(info.Path) != info.Path || strings.ContainsAny(info.Path, "\\?#%") {
		return "", ErrRelease
	}
	name := path.Join(path.Dir(info.Path), hex.EncodeToString(info.Hashes["sha256"])+"."+path.Base(info.Path))
	return strings.TrimRight(r.configuration.TargetsURL, "/") + "/" + name, nil
}

func (r *Repository) targetBytes(ctx context.Context, info *metadata.TargetFiles, maximum int64) ([]byte, error) {
	address, err := r.targetURL(info)
	if err != nil {
		return nil, err
	}
	response, err := fetchResponse(ctx, r.client, address)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximum {
		return nil, ErrRelease
	}
	if err := info.VerifyLengthHashes(data); err != nil {
		return nil, err
	}
	return data, nil
}

func (r *Repository) downloadTarget(ctx context.Context, info *metadata.TargetFiles, destination string) error {
	address, err := r.targetURL(info)
	if err != nil {
		return err
	}
	response, err := fetchResponse(ctx, r.client, address)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, info.Length+1))
	if err != nil {
		return err
	}
	if size != info.Length || !bytes.Equal(hash.Sum(nil), info.Hashes["sha256"]) {
		return ErrRelease
	}
	return file.Sync()
}

type repositoryFetcher struct {
	ctx    context.Context
	client *http.Client
	base   string
}

func (f *repositoryFetcher) DownloadFile(address string, maximum int64, _ time.Duration) ([]byte, error) {
	if !strings.HasPrefix(address, strings.TrimRight(f.base, "/")+"/") || maximum <= 0 || maximum > 1<<20 {
		return nil, ErrRelease
	}
	response, err := fetchResponse(f.ctx, f.client, address)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximum {
		return nil, ErrRelease
	}
	return data, nil
}

func fetchResponse(ctx context.Context, client *http.Client, address string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		return nil, &metadata.ErrDownloadHTTP{StatusCode: response.StatusCode, URL: address}
	}
	return response, nil
}

func readPrivate(path string, maximum int64) ([]byte, error) {
	file, err := privatefile.Open(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maximum {
		return nil, ErrRelease
	}
	return data, nil
}

func durableFile(path string, data []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".release-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(file.Name(), path); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func syncMetadata(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.Type().IsRegular() {
			return fmt.Errorf("invalid metadata cache entry: %s", entry.Name())
		}
		file, err := os.OpenFile(filepath.Join(dir, entry.Name()), os.O_RDWR, 0)
		if err != nil {
			return err
		}
		err = errors.Join(file.Chmod(0o600), file.Sync(), file.Close())
		if err != nil {
			return err
		}
	}
	return syncDirectory(dir)
}
