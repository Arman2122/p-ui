package assets

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/Arman2122/p-ui/internal/node/configuration"
)

func testAsset(id, value string) configuration.FileAsset {
	digest := sha256.Sum256([]byte(value))
	return configuration.FileAsset{ID: id, SHA256: hex.EncodeToString(digest[:]), Data: []byte(value)}
}

func assetStore(t *testing.T) (*Store, string) {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	store, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, directory
}

func TestStageKeepsImmutableAppliedAndCandidateFiles(t *testing.T) {
	store, directory := assetStore(t)
	applied, candidate := testAsset("server-key", "original private key"), testAsset("server-key", "rotated private key")
	old, err := store.Stage([]configuration.FileAsset{applied})
	if err != nil {
		t.Fatal(err)
	}
	current, err := store.Stage([]configuration.FileAsset{candidate})
	if err != nil {
		t.Fatal(err)
	}
	for _, asset := range []configuration.FileAsset{applied, candidate} {
		path := filepath.Join(directory, "assets", asset.SHA256)
		data, err := os.ReadFile(path)
		var stat unix.Stat_t
		if err != nil || !bytes.Equal(data, asset.Data) || unix.Lstat(path, &stat) != nil || stat.Mode&0o7777 != 0o600 || stat.Nlink != 1 || stat.Uid != uint32(os.Geteuid()) {
			t.Fatalf("asset lost content or private ownership: %v", err)
		}
	}
	if old[configuration.AssetReference(applied.ID, applied.SHA256)] != filepath.Join(directory, "assets", applied.SHA256) || current[configuration.AssetReference(candidate.ID, candidate.SHA256)] != filepath.Join(directory, "assets", candidate.SHA256) {
		t.Fatal("asset references are not immutable digest paths")
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	duplicate := applied
	duplicate.ID = "another-authorized-role"
	if paths, err := reopened.Stage([]configuration.FileAsset{applied, duplicate}); err != nil || len(paths) != 2 || paths[configuration.AssetReference(duplicate.ID, duplicate.SHA256)] != old[configuration.AssetReference(applied.ID, applied.SHA256)] {
		t.Fatalf("reopen or content dedup failed: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(directory, "assets"))
	if err != nil || len(entries) != 2 {
		t.Fatalf("successful staging left temporary or duplicate files: %d %v", len(entries), err)
	}
}

func TestStageRejectsWholeMalformedBatchBeforeWriting(t *testing.T) {
	store, directory := assetStore(t)
	first, bad := testAsset("first", "one"), testAsset("bad", "two")
	bad.Data = []byte("forged bytes")
	if paths, err := store.Stage([]configuration.FileAsset{first, bad}); !errors.Is(err, configuration.ErrConfiguration) || paths != nil {
		t.Fatalf("invalid batch produced usable paths: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(directory, "assets"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("partially staged invalid batch: %d %v", len(entries), err)
	}
}

func TestStageCleansFailedTemporaryCreation(t *testing.T) {
	store, directory := assetStore(t)
	previous := unix.Umask(0o777)
	defer unix.Umask(previous)
	if paths, err := store.Stage([]configuration.FileAsset{testAsset("key", "private")}); !errors.Is(err, ErrFilesystem) || paths != nil {
		t.Fatalf("insecure created file was admitted: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(directory, "assets"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed staged creation leaked temporary files: %d %v", len(entries), err)
	}
}

func TestStageRefusesUnsafeExistingFilesWithoutReplacingThem(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink", "mode", "fifo", "directory", "content", "owner"} {
		t.Run(kind, func(t *testing.T) {
			store, directory := assetStore(t)
			asset := testAsset("key", "authorized private material")
			path := filepath.Join(directory, "assets", asset.SHA256)
			outside := filepath.Join(directory, "outside")
			outsideData := []byte("outside sentinel")
			if kind == "hardlink" {
				outsideData = asset.Data
			}
			if err := os.WriteFile(outside, outsideData, 0o600); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "symlink":
				err = os.Symlink(outside, path)
			case "hardlink":
				err = os.Link(outside, path)
			case "fifo":
				err = unix.Mkfifo(path, 0o600)
			case "directory":
				err = os.Mkdir(path, 0o700)
			default:
				data := asset.Data
				if kind == "content" {
					data = []byte("unexpected existing material")
				}
				err = os.WriteFile(path, data, 0o600)
				if err == nil && kind == "mode" {
					err = os.Chmod(path, 0o640)
				}
				if err == nil && kind == "owner" {
					if os.Geteuid() != 0 {
						t.Skip("foreign-owner creation requires an isolated root test")
					}
					err = os.Chown(path, 65534, -1)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			var before, after unix.Stat_t
			if err := unix.Lstat(path, &before); err != nil {
				t.Fatal(err)
			}
			paths, err := store.Stage([]configuration.FileAsset{asset})
			if err == nil || paths != nil {
				t.Fatalf("unsafe %s asset was admitted", kind)
			}
			if kind == "content" && !errors.Is(err, ErrContent) {
				t.Fatalf("corrupt content diagnosis: %v", err)
			}
			if unix.Lstat(path, &after) != nil || before.Ino != after.Ino || before.Mode != after.Mode {
				t.Fatal("rejected existing asset was replaced")
			}
			if data, err := os.ReadFile(outside); err != nil || !bytes.Equal(data, outsideData) {
				t.Fatal("asset filesystem escaped to an unrelated file")
			}
		})
	}
}

func TestStoreRefusesSymlinkedOrReplacedDirectories(t *testing.T) {
	for _, kind := range []string{"root-symlink", "parent-symlink", "assets-symlink", "root-replaced", "assets-replaced", "root-mode", "assets-mode"} {
		t.Run(kind, func(t *testing.T) {
			store, directory := assetStore(t)
			outside := t.TempDir()
			if err := os.Chmod(outside, 0o700); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "root-symlink", "root-replaced":
				err = os.Rename(directory, directory+"-original")
				defer os.RemoveAll(directory + "-original")
				if err == nil && kind == "root-symlink" {
					err = os.Symlink(outside, directory)
				} else if err == nil {
					err = os.Mkdir(directory, 0o700)
				}
			case "parent-symlink":
				link := filepath.Join(outside, "alias")
				if err := os.Symlink(filepath.Dir(directory), link); err != nil {
					t.Fatal(err)
				}
				if alias, err := Open(filepath.Join(link, filepath.Base(directory))); !errors.Is(err, ErrFilesystem) {
					if alias != nil {
						_ = alias.Close()
					}
					t.Fatalf("ancestor symlink was accepted: %v", err)
				}
				return
			case "assets-symlink", "assets-replaced":
				err = os.Rename(filepath.Join(directory, "assets"), filepath.Join(directory, "old-assets"))
				if err == nil && kind == "assets-symlink" {
					err = os.Symlink(outside, filepath.Join(directory, "assets"))
				} else if err == nil {
					err = os.Mkdir(filepath.Join(directory, "assets"), 0o700)
				}
			case "root-mode":
				err = os.Chmod(directory, 0o750)
			case "assets-mode":
				err = os.Chmod(filepath.Join(directory, "assets"), 0o750)
			}
			if err != nil {
				t.Fatal(err)
			}
			if paths, err := store.Stage([]configuration.FileAsset{testAsset("key", "private")}); !errors.Is(err, ErrFilesystem) || paths != nil {
				t.Fatalf("replaced or insecure directory accepted: %v", err)
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 0 {
				t.Fatal("staging wrote through the substituted directory")
			}
		})
	}
}

func TestConcurrentStoresPublishOneVerifiedImmutableFile(t *testing.T) {
	store, directory := assetStore(t)
	second, err := Open(directory)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	asset := testAsset("certificate", strings.Repeat("material", 32768))
	var group sync.WaitGroup
	for i := range 8 {
		group.Go(func() {
			chosen := store
			if i%2 != 0 {
				chosen = second
			}
			if _, err := chosen.Stage([]configuration.FileAsset{asset}); err != nil {
				t.Error(err)
			}
		})
	}
	group.Wait()
	entries, err := os.ReadDir(filepath.Join(directory, "assets"))
	if err != nil || len(entries) != 1 || entries[0].Name() != asset.SHA256 {
		t.Fatalf("concurrent publication left debris: %d %v", len(entries), err)
	}
}

func TestPrunePreservesAppliedAndDesiredAndRemovesBoundedOrphans(t *testing.T) {
	store, directory := assetStore(t)
	applied, desired, orphan := testAsset("applied", "old"), testAsset("desired", "new"), testAsset("orphan", "unreferenced")
	if _, err := store.Stage([]configuration.FileAsset{applied, desired, orphan}); err != nil {
		t.Fatal(err)
	}
	staging := filepath.Join(directory, "assets", ".stage-"+strings.Repeat("a", 32))
	if err := os.WriteFile(staging, []byte("interrupted write"), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if removed, err := store.Prune([]string{applied.SHA256, desired.SHA256}, 1); err != nil || removed != 1 {
			t.Fatalf("bounded cleanup failed: %d %v", removed, err)
		}
	}
	for _, asset := range []configuration.FileAsset{applied, desired} {
		if data, err := os.ReadFile(filepath.Join(directory, "assets", asset.SHA256)); err != nil || !bytes.Equal(data, asset.Data) {
			t.Fatal("cleanup removed applied or desired authority")
		}
	}
	if removed, err := store.Prune([]string{"../escape"}, 1); !errors.Is(err, ErrContent) || removed != 0 {
		t.Fatalf("unsafe cleanup selector: %d %v", removed, err)
	}
	if err := os.Symlink(filepath.Join(directory, "assets", applied.SHA256), staging); err != nil {
		t.Fatal(err)
	}
	if removed, err := store.Prune([]string{applied.SHA256, desired.SHA256}, 1); err == nil || removed != 0 {
		t.Fatalf("cleanup followed an unsafe staged path: %d %v", removed, err)
	}
}
