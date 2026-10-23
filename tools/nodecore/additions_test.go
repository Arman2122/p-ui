package main

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFacadeChecksumAndUpstreamCollisionRefuseBuild(t *testing.T) {
	module, directory := t.TempDir(), t.TempDir()
	data := []byte("package facade\n\nconst Bound = 1\n")
	file := filepath.Join(directory, "facade.go.patch")
	if err := os.WriteFile(file, data, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	addition := patchAddition{Path: "facade/api.go", Source: "facade.go.patch", SHA256: hex.EncodeToString(digest[:]), Reason: "test bounded facade"}
	patched := map[string][]byte{}
	if err := loadAdditions(patched, []patchAddition{addition}, module, directory); err != nil || string(patched[addition.Path]) != string(data) {
		t.Fatalf("verified facade = %q: %v", patched[addition.Path], err)
	}
	if err := os.WriteFile(file, []byte("package facade\n\nconst Bound = 2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := loadAdditions(map[string][]byte{}, []patchAddition{addition}, module, directory); err == nil || !strings.Contains(err.Error(), "checksum differs") {
		t.Fatalf("changed facade was not refused: %v", err)
	}
	if err := os.Mkdir(filepath.Join(module, "facade"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(module, addition.Path), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := loadAdditions(map[string][]byte{}, []patchAddition{addition}, module, directory); err == nil || !strings.Contains(err.Error(), "replace an upstream file") {
		t.Fatalf("existing upstream file was not protected: %v", err)
	}
}
