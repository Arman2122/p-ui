package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
)

type patch struct {
	Module  string
	Version string
	patchFile
	Files        []patchFile
	Additions    []patchAddition
	Dependencies []patch
}

type patchAddition struct {
	Path   string
	Source string
	SHA256 string
	Reason string
}

type patchFile struct {
	Path         string
	SHA256       string
	Reason       string
	Replacements []struct{ From, To string }
}

func run() error {
	coreName := flag.String("core", "singbox", "governed runtime: xray, singbox or mtproto")
	goCommand := flag.String("go", "", "Xray root-module Go command: test, build, list, run, vet")
	toolCommand := flag.String("tool", "", "Xray root-module tool: govulncheck")
	race := flag.Bool("race", false, "enable race detection")
	prepareOnly := flag.Bool("prepare-only", false, "print patched module file without running tests")
	runPattern := flag.String("run", "", "test selection expression")
	buildOutput := flag.String("build", "", "build the governed runtime executable to this path")
	lint := flag.Bool("lint", false, "lint the governed runtime against its pinned patched module")
	flag.Parse()
	if *buildOutput != "" && (*race || *lint) || *prepareOnly && *lint {
		return fmt.Errorf("build, lint, prepare-only and race-test modes cannot be combined")
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	if *coreName == "xray" {
		return runXray(ctx, root, xrayOptions{race: *race, prepare: *prepareOnly, pattern: *runPattern, output: *buildOutput, lint: *lint, goCommand: *goCommand, toolCommand: *toolCommand, args: flag.Args()})
	}
	if *goCommand != "" || *toolCommand != "" || len(flag.Args()) != 0 {
		return fmt.Errorf("root module command mode requires xray")
	}
	var moduleName, version, manifest string
	switch *coreName {
	case "singbox":
		moduleName, version, manifest = "github.com/sagernet/sing-box", "v1.14.0", "network-started.json"
	case "mtproto":
		moduleName, version, manifest = "github.com/Arman2122/mtg-proxy", "v1.9.0", "governor.json"
	default:
		return fmt.Errorf("unsupported core %q", *coreName)
	}
	moduleDir := filepath.Join(root, "core-runtimes", *coreName)
	raw, err := os.ReadFile(filepath.Join(moduleDir, "patches", manifest))
	if err != nil {
		return err
	}
	var spec patch
	if err := json.Unmarshal(raw, &spec); err != nil {
		return err
	}
	if spec.Module != moduleName || spec.Version != version {
		return fmt.Errorf("unsupported patch target")
	}
	if *coreName != "singbox" && len(spec.Dependencies) != 0 {
		return fmt.Errorf("unsupported dependency patch target")
	}
	download := exec.CommandContext(ctx, "go", "mod", "download", spec.Module+"@"+spec.Version)
	download.Dir = moduleDir
	if result, err := download.CombinedOutput(); err != nil {
		return fmt.Errorf("download pinned runtime: %w: %s", err, result)
	}
	cmd := exec.CommandContext(ctx, "go", "list", "-m", "-json", spec.Module)
	cmd.Dir = moduleDir
	raw, err = cmd.Output()
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
	if module.Version != spec.Version || module.Dir == "" || module.Replace != nil {
		return fmt.Errorf("unreviewed core module version or replacement")
	}
	files := spec.Files
	if len(files) == 0 {
		files = []patchFile{spec.patchFile}
	}
	patched := make(map[string][]byte, len(files))
	for _, file := range files {
		if !filepath.IsLocal(file.Path) {
			return fmt.Errorf("invalid patch path")
		}
		if _, exists := patched[file.Path]; exists {
			return fmt.Errorf("duplicate patch path")
		}
		data, err := os.ReadFile(filepath.Join(module.Dir, filepath.FromSlash(file.Path)))
		if err != nil {
			return err
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != file.SHA256 {
			return fmt.Errorf("core patch source checksum differs: %s", file.Path)
		}
		for _, edit := range file.Replacements {
			if edit.From == "" || bytes.Count(data, []byte(edit.From)) != 1 {
				return fmt.Errorf("core patch context differs: %s", file.Path)
			}
			data = bytes.Replace(data, []byte(edit.From), []byte(edit.To), 1)
		}
		data, err = format.Source(data)
		if err != nil {
			return err
		}
		patched[file.Path] = data
	}
	if err := loadAdditions(patched, spec.Additions, module.Dir, filepath.Join(moduleDir, "patches")); err != nil {
		return err
	}
	cacheDir := filepath.Join(root, ".cache", "node-core")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	cacheDir, err = filepath.EvalSymlinks(cacheDir)
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(realRoot, cacheDir)
	if err != nil || filepath.ToSlash(relative) != ".cache/node-core" {
		return fmt.Errorf("runtime cache must remain inside the workspace")
	}
	outputDir, err := os.MkdirTemp(cacheDir, *coreName+"-")
	if err != nil {
		return err
	}
	if !*prepareOnly {
		defer os.RemoveAll(outputDir)
	}
	sourceDir := filepath.Join(outputDir, "source")
	if err := copyModule(module.Dir, sourceDir); err != nil {
		return err
	}
	for name, data := range patched {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(sourceDir, filepath.FromSlash(name))), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(sourceDir, filepath.FromSlash(name)), data, 0o600); err != nil {
			return err
		}
	}
	modFile := filepath.Join(outputDir, "runtime.mod")
	for _, pair := range [][2]string{{"go.mod", "runtime.mod"}, {"go.sum", "runtime.sum"}} {
		content, err := os.ReadFile(filepath.Join(moduleDir, pair[0]))
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(outputDir, pair[1]), content, 0o600); err != nil {
			return err
		}
	}
	dependencies, err := prepareDependencies(ctx, moduleDir, outputDir, spec.Dependencies)
	if err != nil {
		return err
	}
	editArgs := []string{
		"mod", "edit", "-modfile", modFile,
		"-replace", spec.Module + "=" + filepath.ToSlash(sourceDir),
		"-replace", "github.com/Arman2122/p-ui=" + filepath.ToSlash(root),
	}
	editArgs = append(editArgs, dependencies...)
	edit := exec.CommandContext(ctx, "go", editArgs...)
	edit.Dir = moduleDir
	if result, err := edit.CombinedOutput(); err != nil {
		return fmt.Errorf("prepare patched module: %w: %s", err, result)
	}
	if *prepareOnly {
		fmt.Println(modFile)
		return nil
	}
	if *lint {
		lintDir := filepath.Join(outputDir, "lint-module")
		if err := copyModule(moduleDir, lintDir); err != nil {
			return err
		}
		for _, pair := range [][2]string{{"runtime.mod", "go.mod"}, {"runtime.sum", "go.sum"}} {
			data, err := os.ReadFile(filepath.Join(outputDir, pair[0]))
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(lintDir, pair[1]), data, 0o600); err != nil {
				return err
			}
		}
		args := []string{"run", "--config", filepath.Join(root, ".golangci.yml")}
		if *coreName == "singbox" {
			args = append(args, "--build-tags", "with_quic")
		}
		args = append(args, "./...")
		cmd := exec.CommandContext(ctx, "golangci-lint", args...)
		cmd.Dir = lintDir
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		return cmd.Run()
	}
	args := []string{"test", "-modfile", modFile, "-count=1", "-timeout=3m"}
	if *coreName == "singbox" {
		args = append(args, "-tags", "with_quic")
	}
	if *race {
		args = append(args, "-race")
	}
	if *runPattern != "" {
		args = append(args, "-run", *runPattern)
	}
	args = append(args, "./...")
	if *buildOutput != "" {
		output, err := filepath.Abs(*buildOutput)
		if err != nil {
			return err
		}
		args = []string{"build", "-modfile", modFile, "-trimpath", "-o", output}
		if *coreName == "singbox" {
			args = append(args, "-tags", "with_quic")
		}
		args = append(args, "./cmd/penhoon-"+*coreName)
	}
	cmd = exec.CommandContext(ctx, "go", args...)
	cmd.Dir = moduleDir
	cmd.Env = os.Environ()
	if *buildOutput == "" {
		flags, err := testGoFlags(os.Getenv("GOFLAGS"), nil)
		if err != nil {
			return err
		}
		cmd.Env = append(cmd.Env, "GOFLAGS="+flags)
	}
	if *buildOutput != "" {
		cmd.Env = append(cmd.Env, "CGO_ENABLED=0")
	}
	if *race {
		cmd.Env = append(cmd.Env, "CGO_ENABLED=1")
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func copyModule(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil || !filepath.IsLocal(rel) {
			return fmt.Errorf("invalid module path: %s", path)
		}
		output := filepath.Join(target, rel)
		if entry.IsDir() {
			return os.MkdirAll(output, 0o700)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("unsupported module file: %s", path)
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(output, content, 0o600)
	})
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
