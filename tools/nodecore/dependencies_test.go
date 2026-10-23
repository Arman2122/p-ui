package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuthenticationDependencyPreparationRejectsSourceDrift(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	moduleDir := filepath.Join(root, "core-runtimes/singbox")
	flags, _, err := moduleGoFlags(moduleDir, os.Getenv("GOFLAGS"), "")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOFLAGS", flags)
	raw, err := os.ReadFile(filepath.Join(moduleDir, "patches/network-started.json"))
	if err != nil {
		t.Fatal(err)
	}
	var spec patch
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	if len(spec.Dependencies) != 2 {
		t.Fatal("both authentication service dependencies must be pinned")
	}
	beforeMod, err := os.ReadFile(filepath.Join(moduleDir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	beforeSum, err := os.ReadFile(filepath.Join(moduleDir, "go.sum"))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := prepareDependencies(t.Context(), moduleDir, t.TempDir(), spec.Dependencies)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared) != 4 {
		t.Fatalf("dependency replacement flags=%q", prepared)
	}
	for _, index := range []int{1, 3} {
		_, directory, found := strings.Cut(prepared[index], "=")
		if !found {
			t.Fatal("prepared authentication dependency has no source directory")
		}
		file := "service.go"
		if index == 1 {
			file = "tuic/service.go"
		}
		source, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(file)))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(source, []byte("const PenhoonAuthenticationPatchLevel = 1")) {
			t.Fatal("prepared dependency lost immutable authentication patch")
		}
	}
	spec.Dependencies[0].Files[0].SHA256 = strings.Repeat("0", 64)
	_, err = prepareDependencies(t.Context(), moduleDir, t.TempDir(), spec.Dependencies)
	if err == nil || err.Error() != "authentication dependency checksum differs: tuic/service.go" {
		t.Fatalf("authentication dependency accepted checksum drift: %v", err)
	}
	afterMod, _ := os.ReadFile(filepath.Join(moduleDir, "go.mod"))
	afterSum, _ := os.ReadFile(filepath.Join(moduleDir, "go.sum"))
	if !bytes.Equal(beforeMod, afterMod) || !bytes.Equal(beforeSum, afterSum) {
		t.Fatal("authentication preparation modified repository module files")
	}
}
