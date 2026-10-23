package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
)

func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	protoc := flag.String("protoc", "protoc", "protobuf compiler path")
	plugin := flag.String("plugin", "protoc-gen-go", "Go generator path")
	grpcPlugin := flag.String("grpc-plugin", "protoc-gen-go-grpc", "gRPC generator path")
	check := flag.Bool("check", false, "check bindings without writing")
	flag.Parse()
	for _, tool := range []struct{ path, want string }{{*protoc, "libprotoc 33.2"}, {*plugin, "protoc-gen-go v1.36.12"}, {*grpcPlugin, "protoc-gen-go-grpc 1.5.1"}} {
		output, err := exec.CommandContext(ctx, tool.path, "--version").CombinedOutput()
		if err != nil {
			return fmt.Errorf("verify %s: %w: %s", tool.path, err, output)
		}
		version := strings.ReplaceAll(strings.TrimSpace(string(output)), ".exe ", " ")
		if version != tool.want {
			return fmt.Errorf("unexpected generator version: %s", output)
		}
	}
	pluginPath, err := exec.LookPath(*plugin)
	if err != nil {
		return err
	}
	pluginPath, err = filepath.Abs(pluginPath)
	if err != nil {
		return err
	}
	grpcPath, err := exec.LookPath(*grpcPlugin)
	if err != nil {
		return err
	}
	grpcPath, err = filepath.Abs(grpcPath)
	if err != nil {
		return err
	}
	outputDir, err := os.MkdirTemp("", "penhoon-node-proto-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(outputDir)
	sources := []string{"internal/cores/internal/xray/enforcement/config.proto", "api/node/v1/node.proto", "api/node/v1/time.proto", "api/node/v1/credit.proto", "api/node/v1/authority_update.proto"}
	args := []string{"--plugin=protoc-gen-go=" + pluginPath, "--plugin=protoc-gen-go-grpc=" + grpcPath, "--go_out=" + outputDir, "--go_opt=paths=source_relative", "--go-grpc_out=" + outputDir, "--go-grpc_opt=paths=source_relative"}
	args = append(args, sources...)
	if output, err := exec.CommandContext(ctx, *protoc, args...).CombinedOutput(); err != nil {
		return fmt.Errorf("generate protocol: %w: %s", err, output)
	}
	for _, source := range sources {
		for _, suffix := range []string{".pb.go", "_grpc.pb.go"} {
			target := strings.TrimSuffix(source, ".proto") + suffix
			data, err := os.ReadFile(filepath.Join(outputDir, target))
			if errors.Is(err, os.ErrNotExist) && suffix == "_grpc.pb.go" && source == sources[0] {
				continue
			}
			if err != nil {
				return err
			}
			data, err = normalize(data)
			if err != nil {
				return err
			}
			current, readErr := os.ReadFile(target)
			if readErr == nil && bytes.Equal(current, data) {
				continue
			}
			if *check {
				return fmt.Errorf("generated node protocol is stale: %s", target)
			}
			if err := os.WriteFile(target, data, 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

func normalize(data []byte) ([]byte, error) {
	lines := strings.Split(string(data), "\n")
	var output []string
	for i := 0; i < len(lines); {
		line := lines[i]
		if !strings.HasPrefix(strings.TrimSpace(line), "//") || strings.HasPrefix(strings.TrimSpace(line), "//go:") {
			output = append(output, line)
			i++
			continue
		}
		start := i
		for i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "//") && !strings.HasPrefix(strings.TrimSpace(lines[i]), "//go:") {
			i++
		}
		if i-start <= 2 {
			output = append(output, lines[start:i]...)
			continue
		}
		output = append(output, lines[start])
		indent := lines[start][:len(lines[start])-len(strings.TrimLeft(lines[start], "\t "))]
		parts := make([]string, 0, i-start-1)
		for _, comment := range lines[start+1 : i] {
			text := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(comment), "//"))
			if text != "" {
				parts = append(parts, text)
			}
		}
		output = append(output, indent+"// "+strings.Join(parts, " "))
	}
	return format.Source([]byte(strings.Join(output, "\n")))
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
