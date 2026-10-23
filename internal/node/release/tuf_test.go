package release

import (
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sigstore/sigstore/pkg/signature"
	"github.com/theupdateframework/go-tuf/v2/metadata"
)

type tufFixture struct {
	t          *testing.T
	mu         sync.RWMutex
	files      map[string][]byte
	keys       map[string]ed25519.PrivateKey
	rootKeys   []ed25519.PrivateKey
	root       []byte
	version    int64
	expires    time.Time
	delegation string
	manifest   Manifest
	assets     map[string][]byte
	server     *httptest.Server
	redirect   string
}

func (f *tufFixture) put(name string, data []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if data == nil {
		delete(f.files, name)
	} else {
		f.files[name] = data
	}
}

func tufKey(t *testing.T) ed25519.PrivateKey {
	t.Helper()
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func tufSigned[T metadata.Roles](t *testing.T, value *metadata.Metadata[T], keys ...ed25519.PrivateKey) []byte {
	t.Helper()
	value.Signatures = nil
	for _, key := range keys {
		signer, err := signature.LoadSigner(key, crypto.Hash(0))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := value.Sign(signer); err != nil {
			t.Fatal(err)
		}
	}
	data, err := value.ToBytes(false)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func (f *tufFixture) rootMetadata(version int64, rootKeys []ed25519.PrivateKey) *metadata.Metadata[metadata.RootType] {
	f.t.Helper()
	root := metadata.Root(time.Now().Add(24 * time.Hour))
	root.Signed.Version = version
	for role, private := range f.keys {
		if role == "platform" {
			continue
		}
		key, err := metadata.KeyFromPublicKey(private.Public())
		if err != nil {
			f.t.Fatal(err)
		}
		if err := root.Signed.AddKey(key, role); err != nil {
			f.t.Fatal(err)
		}
	}
	for _, private := range rootKeys {
		key, err := metadata.KeyFromPublicKey(private.Public())
		if err != nil {
			f.t.Fatal(err)
		}
		if err := root.Signed.AddKey(key, "root"); err != nil {
			f.t.Fatal(err)
		}
	}
	root.Signed.Roles["root"].Threshold = 2
	return root
}

func newTUFFixture(t *testing.T) *tufFixture {
	t.Helper()
	f := &tufFixture{t: t, files: make(map[string][]byte), keys: make(map[string]ed25519.PrivateKey), version: 1, expires: time.Now().Add(time.Hour), delegation: "nodes/linux-amd64/*/*", assets: make(map[string][]byte)}
	for _, role := range []string{"targets", "timestamp", "snapshot", "platform"} {
		f.keys[role] = tufKey(t)
	}
	f.rootKeys = []ed25519.PrivateKey{tufKey(t), tufKey(t), tufKey(t)}
	f.root = tufSigned(t, f.rootMetadata(1, f.rootKeys), f.rootKeys[:2]...)
	f.files["/metadata/1.root.json"] = f.root
	dir := t.TempDir()
	for _, name := range binaries {
		f.assets[name] = []byte("synthetic signed artifact: " + name)
		if err := os.WriteFile(filepath.Join(dir, name), f.assets[name], 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var err error
	f.manifest, err = Create(dir, "tuf-test", "linux-amd64", 1)
	if err != nil {
		t.Fatal(err)
	}
	f.publish()
	f.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.RLock()
		data, ok := f.files[r.URL.Path]
		redirect := f.redirect
		f.mu.RUnlock()
		if redirect != "" {
			http.Redirect(w, r, redirect, http.StatusFound)
			return
		}
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(data)
	}))
	t.Cleanup(f.server.Close)
	return f
}

func tufMeta(version int64, data []byte) *metadata.MetaFiles {
	hash := sha256.Sum256(data)
	return &metadata.MetaFiles{Version: version, Length: int64(len(data)), Hashes: metadata.Hashes{"sha256": hash[:]}}
}

func (f *tufFixture) publish() {
	f.t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	top := metadata.Targets(time.Now().Add(time.Hour))
	top.Signed.Version = f.version
	top.Signed.Delegations = &metadata.Delegations{Keys: make(map[string]*metadata.Key), Roles: []metadata.DelegatedRole{{Name: "platform", Threshold: 1, Terminating: true, Paths: []string{f.delegation}}}}
	key, err := metadata.KeyFromPublicKey(f.keys["platform"].Public())
	if err != nil {
		f.t.Fatal(err)
	}
	if err := top.Signed.AddKey(key, "platform"); err != nil {
		f.t.Fatal(err)
	}
	delegated := metadata.Targets(time.Now().Add(time.Hour))
	delegated.Signed.Version = f.version
	manifest, err := json.Marshal(f.manifest)
	if err != nil {
		f.t.Fatal(err)
	}
	payloads := make(map[string][]byte)
	for name, data := range f.assets {
		payloads[name] = data
	}
	payloads["manifest.json"] = manifest
	for name, data := range payloads {
		target := "nodes/linux-amd64/1/" + name
		info, err := metadata.TargetFile().FromBytes(target, data, "sha256")
		if err != nil {
			f.t.Fatal(err)
		}
		delegated.Signed.Targets[target] = info
		f.files["/targets/"+path.Dir(target)+"/"+hex.EncodeToString(info.Hashes["sha256"])+"."+name] = data
	}
	targetData := tufSigned(f.t, top, f.keys["targets"])
	delegatedData := tufSigned(f.t, delegated, f.keys["platform"])
	snapshot := metadata.Snapshot(time.Now().Add(time.Hour))
	snapshot.Signed.Version = f.version
	snapshot.Signed.Meta["targets.json"] = tufMeta(f.version, targetData)
	snapshot.Signed.Meta["platform.json"] = tufMeta(f.version, delegatedData)
	snapshotData := tufSigned(f.t, snapshot, f.keys["snapshot"])
	timestamp := metadata.Timestamp(f.expires)
	timestamp.Signed.Version = f.version
	timestamp.Signed.Meta["snapshot.json"] = tufMeta(f.version, snapshotData)
	f.files["/metadata/timestamp.json"] = tufSigned(f.t, timestamp, f.keys["timestamp"])
	f.files[fmt.Sprintf("/metadata/%d.snapshot.json", f.version)] = snapshotData
	f.files[fmt.Sprintf("/metadata/%d.targets.json", f.version)] = targetData
	f.files[fmt.Sprintf("/metadata/%d.platform.json", f.version)] = delegatedData
}

func (f *tufFixture) client() *Repository {
	f.t.Helper()
	dir := filepath.Join(f.t.TempDir(), "trust")
	if err := InitializeRepository(dir, RepositoryConfig{Version: 1, MetadataURL: f.server.URL + "/metadata", TargetsURL: f.server.URL + "/targets", Platform: "linux-amd64"}, f.root); err != nil {
		f.t.Fatal(err)
	}
	client, err := OpenRepository(dir)
	if err != nil {
		f.t.Fatal(err)
	}
	client.client.Transport = f.server.Client().Transport
	return client
}

func TestTUFFetchEnforcesExpiryRollbackDelegationAndAssetBinding(t *testing.T) {
	t.Run("expiry", func(t *testing.T) {
		f := newTUFFixture(t)
		f.expires = time.Now().Add(-time.Hour)
		f.publish()
		_, err := f.client().Fetch(context.Background(), 1, filepath.Join(t.TempDir(), "release"))
		var expired *metadata.ErrExpiredMetadata
		if !errors.As(err, &expired) {
			t.Fatalf("expired timestamp: %v", err)
		}
	})
	t.Run("rollback persists across reopen", func(t *testing.T) {
		f := newTUFFixture(t)
		f.version = 2
		f.publish()
		client := f.client()
		if _, err := client.Fetch(context.Background(), 1, filepath.Join(t.TempDir(), "good")); err != nil {
			t.Fatal(err)
		}
		f.version = 1
		f.publish()
		reopened, err := OpenRepository(client.dir)
		if err != nil {
			t.Fatal(err)
		}
		reopened.client.Transport = f.server.Client().Transport
		_, err = reopened.Fetch(context.Background(), 1, filepath.Join(t.TempDir(), "bad"))
		var version *metadata.ErrBadVersionNumber
		if !errors.As(err, &version) {
			t.Fatalf("rolled-back timestamp: %v", err)
		}
	})
	t.Run("delegation", func(t *testing.T) {
		f := newTUFFixture(t)
		f.delegation = "nodes/linux-arm64/*/*"
		f.publish()
		_, err := f.client().Fetch(context.Background(), 1, filepath.Join(t.TempDir(), "release"))
		if err == nil || err.Error() != "target nodes/linux-amd64/1/manifest.json not found" {
			t.Fatalf("foreign delegation: %v", err)
		}
	})
	t.Run("artifact set binding", func(t *testing.T) {
		f := newTUFFixture(t)
		f.assets["penhoon-release"] = []byte("different signed helper outside selected manifest")
		f.publish()
		destination := filepath.Join(t.TempDir(), "release")
		_, err := f.client().Fetch(context.Background(), 1, destination)
		if !errors.Is(err, ErrRelease) {
			t.Fatalf("mismatched helper target: %v", err)
		}
		if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("partial activation candidate: %v", err)
		}
	})
	for _, field := range []string{"platform", "sequence"} {
		t.Run("signed manifest "+field, func(t *testing.T) {
			f := newTUFFixture(t)
			if field == "platform" {
				f.manifest.Platform = "linux-arm64"
			} else {
				f.manifest.Sequence = 2
			}
			f.publish()
			_, err := f.client().Fetch(context.Background(), 1, filepath.Join(t.TempDir(), "release"))
			if !errors.Is(err, ErrRelease) {
				t.Fatalf("manifest identity: %v", err)
			}
		})
	}
	t.Run("modified transport payload", func(t *testing.T) {
		f := newTUFFixture(t)
		f.mu.Lock()
		for address, data := range f.files {
			if strings.HasSuffix(address, ".penhoon-agent") {
				f.files[address] = append(append([]byte(nil), data...), 0)
			}
		}
		f.mu.Unlock()
		_, err := f.client().Fetch(context.Background(), 1, filepath.Join(t.TempDir(), "release"))
		if !errors.Is(err, ErrRelease) {
			t.Fatalf("modified artifact: %v", err)
		}
	})
}

func TestTUFRejectsRepositoryRedirects(t *testing.T) {
	f := newTUFFixture(t)
	client := f.client()
	destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("untrusted redirected response")) }))
	defer destination.Close()
	f.mu.Lock()
	f.redirect = destination.URL
	f.mu.Unlock()
	_, err := client.Fetch(context.Background(), 1, filepath.Join(t.TempDir(), "release"))
	if err == nil || !strings.Contains(err.Error(), "release repository redirects are refused") {
		t.Fatalf("redirect escaped configured repository: %v", err)
	}
}

func TestTUFRootRotationRequiresOldAndNewThresholdAndPersists(t *testing.T) {
	f := newTUFFixture(t)
	client := f.client()
	newKeys := []ed25519.PrivateKey{tufKey(t), tufKey(t), tufKey(t)}
	rotated := f.rootMetadata(2, newKeys)
	f.put("/metadata/2.root.json", tufSigned(t, rotated, f.rootKeys[0], newKeys[0], newKeys[1]))
	_, err := client.Fetch(context.Background(), 1, filepath.Join(t.TempDir(), "rejected"))
	var unsigned *metadata.ErrUnsignedMetadata
	if !errors.As(err, &unsigned) {
		t.Fatalf("missing old-root threshold: %v", err)
	}
	f.put("/metadata/2.root.json", tufSigned(t, rotated, f.rootKeys[0], f.rootKeys[1], newKeys[0]))
	_, err = client.Fetch(context.Background(), 1, filepath.Join(t.TempDir(), "rejected-again"))
	if !errors.As(err, &unsigned) {
		t.Fatalf("missing new-root threshold: %v", err)
	}
	f.put("/metadata/2.root.json", tufSigned(t, rotated, f.rootKeys[0], f.rootKeys[1], newKeys[0], newKeys[1]))
	if _, err := client.Fetch(context.Background(), 1, filepath.Join(t.TempDir(), "accepted")); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenRepository(client.dir)
	if err != nil {
		t.Fatal(err)
	}
	reopened.client.Transport = f.server.Client().Transport
	f.put("/metadata/2.root.json", nil)
	if _, err := reopened.Fetch(context.Background(), 1, filepath.Join(t.TempDir(), "after-restart")); err != nil {
		t.Fatalf("rotated trust was not persisted: %v", err)
	}
	if err := InitializeRepository(client.dir, client.configuration, f.root); !errors.Is(err, os.ErrExist) {
		t.Fatalf("bootstrap replaced existing trust: %v", err)
	}
	if err := os.Remove(filepath.Join(client.dir, "metadata", "root.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := reopened.Fetch(context.Background(), 1, filepath.Join(t.TempDir(), "missing-root")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing trust root was silently replaced: %v", err)
	}
}
