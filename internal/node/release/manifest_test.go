package release

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestReleaseRejectsTamperingPlatformAndChangedAssets(t *testing.T) {
	public, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, binary := range binaries {
		if err := os.WriteFile(filepath.Join(dir, binary), []byte("test-binary-"+binary), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	m, err := Create(dir, "v1.0.0", "linux-amd64", 1)
	if err != nil {
		t.Fatal(err)
	}
	data, signature, err := Sign(m, key)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Verify(data, signature, public, "linux-amd64")
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []uint32{1, 2, 4} {
		incompatible := m
		incompatible.StateFormat = format
		encoded, signed, err := Sign(incompatible, key)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Verify(encoded, signed, public, "linux-amd64"); !errors.Is(err, ErrRelease) {
			t.Fatalf("unsupported format %d verified: %v", format, err)
		}
	}
	if _, err := VerifyAndStage(dir, filepath.Join(t.TempDir(), "ready"), data, signature, public, "linux-amd64"); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(data, signature, public, "linux-arm64"); !errors.Is(err, ErrRelease) {
		t.Fatalf("wrong platform: %v", err)
	}
	data[len(data)/2] ^= 1
	if _, err := Verify(data, signature, public, "linux-amd64"); !errors.Is(err, ErrRelease) {
		t.Fatalf("tampered manifest: %v", err)
	}
	data[len(data)/2] ^= 1
	if err := os.WriteFile(filepath.Join(dir, binaries[0]), []byte("replaced-after-verification"), 0o700); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "rejected")
	if _, err := VerifyAndStage(dir, destination, data, signature, public, "linux-amd64"); !errors.Is(err, ErrRelease) {
		t.Fatalf("changed artifact: %v", err)
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial release became visible: %v", err)
	}
}
