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
	"slices"
	"strings"
)

type xrayOptions struct {
	race, prepare, lint                     bool
	pattern, output, goCommand, toolCommand string
	args                                    []string
}

func (o xrayOptions) command(root string) (string, []string, error) {
	if o.goCommand != "" || o.toolCommand != "" {
		if o.race || o.prepare || o.pattern != "" || o.output != "" || o.lint || o.goCommand != "" && o.toolCommand != "" {
			return "", nil, fmt.Errorf("choose one Xray command mode")
		}
		if o.toolCommand != "" {
			if o.toolCommand != "govulncheck" {
				return "", nil, fmt.Errorf("unsupported Xray analysis tool")
			}
			args := o.args
			if len(args) == 0 {
				args = []string{"./..."}
			}
			return o.toolCommand, args, nil
		}
		if !slices.Contains([]string{"test", "build", "list", "run", "vet"}, o.goCommand) {
			return "", nil, fmt.Errorf("unsupported Go command")
		}
		for _, arg := range o.args {
			if strings.TrimLeft(strings.SplitN(arg, "=", 2)[0], "-") == "modfile" {
				return "", nil, fmt.Errorf("explicit module file would bypass the pinned patch")
			}
		}
		return "go", append([]string{o.goCommand}, o.args...), nil
	}
	if o.lint {
		args := []string{"run", "--config", filepath.Join(root, ".golangci.yml")}
		if len(o.args) == 0 {
			o.args = []string{"./..."}
		}
		return "golangci-lint", append(args, o.args...), nil
	}
	if len(o.args) != 0 {
		return "", nil, fmt.Errorf("unexpected Xray command arguments")
	}
	if o.output != "" {
		output, err := filepath.Abs(o.output)
		return "go", []string{"build", "-trimpath", "-o", output, "./cmd/penhoon-xray"}, err
	}
	args := []string{"test", "-count=1", "-timeout=3m"}
	if o.race {
		args = append(args, "-race")
	}
	if o.pattern != "" {
		args = append(args, "-run", o.pattern)
	}
	return "go", append(args, "./internal/cores/internal/xray/enforcement", "./internal/cores/noderuntime/xray"), nil
}

func runXray(ctx context.Context, root string, options xrayOptions) error {
	program, args, err := options.command(root)
	if err != nil {
		return err
	}
	workspaceProbe := exec.CommandContext(ctx, "go", "env", "GOWORK")
	workspaceProbe.Dir = root
	workspace, err := workspaceProbe.Output()
	if err != nil {
		return err
	}
	if current := strings.TrimSpace(string(workspace)); current != "" && current != "off" {
		return fmt.Errorf("existing Go workspace conflicts with the pinned Xray build; explicitly select GOWORK=off")
	}
	manifest, err := os.ReadFile(filepath.Join(root, "core-runtimes/xray/patches/concurrency.json"))
	if err != nil {
		return err
	}
	var spec patch
	if err := json.Unmarshal(manifest, &spec); err != nil {
		return err
	}
	if spec.Module != "github.com/xtls/xray-core" || spec.Version == "" || len(spec.Files) == 0 || len(spec.Files) > 16 || len(spec.Additions) != 0 || len(spec.Dependencies) > 1 {
		return fmt.Errorf("unsupported Xray patch manifest")
	}
	if len(spec.Dependencies) != 0 && (spec.Dependencies[0].Module != "github.com/sagernet/sing-shadowsocks" || spec.Dependencies[0].Version != "v0.2.9") {
		return fmt.Errorf("unsupported Xray authentication dependency")
	}
	download := exec.CommandContext(ctx, "go", "mod", "download", spec.Module+"@"+spec.Version)
	download.Dir = root
	if result, err := download.CombinedOutput(); err != nil {
		return fmt.Errorf("download pinned Xray: %w: %s", err, result)
	}
	command := exec.CommandContext(ctx, "go", "list", "-m", "-json", spec.Module)
	command.Dir = root
	raw, err := command.Output()
	if err != nil {
		return err
	}
	var module struct {
		Dir, Version string
		Replace      *json.RawMessage
	}
	if err := json.Unmarshal(raw, &module); err != nil {
		return err
	}
	if module.Version != spec.Version || module.Replace != nil || module.Dir == "" {
		return fmt.Errorf("unreviewed Xray version or replacement")
	}
	patched := make(map[string][]byte, len(spec.Files))
	for _, file := range spec.Files {
		if !filepath.IsLocal(file.Path) || patched[file.Path] != nil {
			return fmt.Errorf("invalid or repeated Xray patch path")
		}
		data, err := os.ReadFile(filepath.Join(module.Dir, filepath.FromSlash(file.Path)))
		if err != nil {
			return err
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != file.SHA256 {
			return fmt.Errorf("Xray patch checksum differs: %s", file.Path)
		}
		for _, edit := range file.Replacements {
			if edit.From == "" || bytes.Count(data, []byte(edit.From)) != 1 {
				return fmt.Errorf("Xray patch context differs: %s", file.Path)
			}
			data = bytes.Replace(data, []byte(edit.From), []byte(edit.To), 1)
		}
		data, err = format.Source(data)
		if err != nil {
			return err
		}
		patched[file.Path] = data
	}
	cache := filepath.Join(root, ".cache/node-core")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		return err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	realCache, err := filepath.EvalSymlinks(cache)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(realRoot, realCache)
	if err != nil || filepath.ToSlash(relative) != ".cache/node-core" {
		return fmt.Errorf("Xray cache must remain inside workspace")
	}
	output, err := os.MkdirTemp(realCache, "xray-")
	if err != nil {
		return err
	}
	if !options.prepare {
		defer os.RemoveAll(output)
	}
	source := filepath.Join(output, "source")
	if err := copyModule(module.Dir, source); err != nil {
		return err
	}
	for name, data := range patched {
		if err := os.WriteFile(filepath.Join(source, filepath.FromSlash(name)), data, 0o600); err != nil {
			return err
		}
	}
	modFile := filepath.Join(output, "runtime.mod")
	flags, selected, err := moduleGoFlags(root, os.Getenv("GOFLAGS"), modFile)
	if err != nil {
		return err
	}
	if program == "go" && len(args) != 0 && args[0] == "test" {
		flags, err = testGoFlags(flags, args[1:])
		if err != nil {
			return err
		}
	}
	for _, pair := range [][2]string{{selected, modFile}, {strings.TrimSuffix(selected, ".mod") + ".sum", strings.TrimSuffix(modFile, ".mod") + ".sum"}} {
		data, err := os.ReadFile(pair[0])
		if err != nil {
			return err
		}
		if err := os.WriteFile(pair[1], data, 0o600); err != nil {
			return err
		}
	}
	dependencies, err := prepareDependencies(ctx, root, output, spec.Dependencies)
	if err != nil {
		return err
	}
	editArgs := []string{"mod", "edit", "-modfile", modFile, "-replace", spec.Module + "=" + filepath.ToSlash(source)}
	editArgs = append(editArgs, dependencies...)
	edit := exec.CommandContext(ctx, "go", editArgs...)
	edit.Dir = root
	if result, err := edit.CombinedOutput(); err != nil {
		return fmt.Errorf("prepare patched Xray module: %w: %s", err, result)
	}
	if options.prepare {
		fmt.Println(modFile)
		return nil
	}
	command = exec.CommandContext(ctx, program, args...)
	command.Dir = root
	command.Env = append(os.Environ(), "GOFLAGS="+flags)
	if options.lint || options.toolCommand != "" {
		command.Env, err = xrayAnalysisEnvironment(ctx, root, output, source, selected, dependencies)
		if err != nil {
			return err
		}
	}
	if options.output != "" {
		command.Env = append(command.Env, "CGO_ENABLED=0")
	}
	if options.race {
		command.Env = append(command.Env, "CGO_ENABLED=1")
	}
	command.Stdout, command.Stderr = os.Stdout, os.Stderr
	return command.Run()
}

func xrayAnalysisEnvironment(ctx context.Context, root, output, source, selected string, dependencies []string) ([]string, error) {
	if filepath.Clean(selected) != filepath.Join(root, "go.mod") {
		return nil, fmt.Errorf("analysis tools require the repository module file")
	}
	flags, _, err := moduleGoFlags(root, os.Getenv("GOFLAGS"), "")
	if err != nil {
		return nil, err
	}
	workspace := filepath.Join(output, "go.work")
	replacements := []string{"work", "edit", "-replace", "github.com/xtls/xray-core=" + filepath.ToSlash(source)}
	replacements = append(replacements, dependencies...)
	for _, args := range [][]string{{"work", "init", root}, replacements} {
		command := exec.CommandContext(ctx, "go", args...)
		command.Dir = output
		command.Env = append(os.Environ(), "GOFLAGS=", "GOWORK="+workspace)
		if result, err := command.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("prepare Xray analysis workspace: %w: %s", err, result)
		}
	}
	return append(os.Environ(), "GOFLAGS="+flags, "GOWORK="+workspace), nil
}
