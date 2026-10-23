package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
)

func prepareDependencies(ctx context.Context, moduleDir, outputDir string, dependencies []patch) ([]string, error) {
	if len(dependencies) > 2 {
		return nil, fmt.Errorf("too many pinned authentication dependencies")
	}
	allowed := map[string]string{"github.com/sagernet/sing-quic": "v0.7.0-beta.4", "github.com/anytls/sing-anytls": "v0.0.11", "github.com/sagernet/sing-shadowsocks": "v0.2.9"}
	var replacements []string
	for index, dependency := range dependencies {
		if dependency.Version == "" || allowed[dependency.Module] != dependency.Version || len(dependency.Files) == 0 || len(dependency.Files) > 4 || len(dependency.Additions) != 0 || len(dependency.Dependencies) != 0 {
			return nil, fmt.Errorf("unreviewed authentication dependency")
		}
		delete(allowed, dependency.Module)
		download := exec.CommandContext(ctx, "go", "mod", "download", dependency.Module+"@"+dependency.Version)
		download.Dir = moduleDir
		if data, err := download.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("download authentication dependency: %w: %s", err, data)
		}
		command := exec.CommandContext(ctx, "go", "list", "-m", "-json", dependency.Module)
		command.Dir = moduleDir
		data, err := command.Output()
		if err != nil {
			return nil, err
		}
		var module struct {
			Dir, Version string
			Replace      *json.RawMessage
		}
		if err := json.Unmarshal(data, &module); err != nil {
			return nil, err
		}
		if module.Version != dependency.Version || module.Dir == "" || module.Replace != nil {
			return nil, fmt.Errorf("authentication dependency pin differs")
		}
		patched := make(map[string][]byte, len(dependency.Files))
		for _, file := range dependency.Files {
			if !filepath.IsLocal(file.Path) || patched[file.Path] != nil {
				return nil, fmt.Errorf("invalid authentication dependency patch path")
			}
			data, err := os.ReadFile(filepath.Join(module.Dir, filepath.FromSlash(file.Path)))
			if err != nil {
				return nil, err
			}
			digest := sha256.Sum256(data)
			if hex.EncodeToString(digest[:]) != file.SHA256 {
				return nil, fmt.Errorf("authentication dependency checksum differs: %s", file.Path)
			}
			for _, edit := range file.Replacements {
				if edit.From == "" || bytes.Count(data, []byte(edit.From)) != 1 {
					return nil, fmt.Errorf("authentication dependency context differs: %s", file.Path)
				}
				data = bytes.Replace(data, []byte(edit.From), []byte(edit.To), 1)
			}
			data, err = format.Source(data)
			if err != nil {
				return nil, err
			}
			patched[file.Path] = data
		}
		target := filepath.Join(outputDir, fmt.Sprintf("dependency-%d", index))
		if err := copyModule(module.Dir, target); err != nil {
			return nil, err
		}
		for path, data := range patched {
			if err := os.WriteFile(filepath.Join(target, filepath.FromSlash(path)), data, 0o600); err != nil {
				return nil, err
			}
		}
		replacements = append(replacements, "-replace", dependency.Module+"="+filepath.ToSlash(target))
	}
	return replacements, nil
}
