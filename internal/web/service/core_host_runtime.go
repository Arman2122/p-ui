package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Arman2122/p-ui/internal/config"
)

func observeCoreHostRuntimes(ctx context.Context) map[string]*CoreHostRuntimeView {
	if runtime.GOOS != "linux" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	return readCoreHostRuntimes(ctx, "/proc", coreExecutablePaths())
}

// Process identity comes from resolved executable paths, never process titles.
func coreExecutablePaths() map[string][]string {
	names := map[string][]string{
		"mtproto": {"mtg-proxy", "mtg", "mtg-linux-" + runtime.GOARCH},
		"singbox": {"sing-box", "sing-box-linux-" + runtime.GOARCH},
		"openvpn": {"openvpn"},
		"ocserv":  {"ocserv"},
		"xray":    {"xray", "xray-linux-" + runtime.GOARCH},
	}
	paths := make(map[string][]string, len(names))
	for id, binaries := range names {
		seen := map[string]bool{}
		for _, binary := range binaries {
			candidates := []string{filepath.Join(config.GetBinFolderPath(), binary)}
			for _, dir := range []string{"/usr/local/bin", "/usr/local/sbin", "/usr/bin", "/usr/sbin"} {
				candidates = append(candidates, filepath.Join(dir, binary))
			}
			if path, err := exec.LookPath(binary); err == nil {
				candidates = append(candidates, path)
			}
			for _, path := range candidates {
				absolute, err := filepath.Abs(path)
				if err != nil {
					continue
				}
				if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
					absolute = resolved
				}
				if !seen[absolute] {
					paths[id] = append(paths[id], absolute)
					seen[absolute] = true
				}
			}
		}
	}
	return paths
}

func readCoreHostRuntimes(ctx context.Context, root string, paths map[string][]string) map[string]*CoreHostRuntimeView {
	views := make(map[string]*CoreHostRuntimeView, len(paths))
	owners := map[string]string{}
	for id, binaries := range paths {
		views[id] = &CoreHostRuntimeView{State: "stopped", Processes: []CoreHostProcessView{}}
		for _, path := range binaries {
			owners[filepath.Clean(path)] = id
		}
	}
	incomplete := func(err error) {
		for _, view := range views {
			view.Error = "Host process observation incomplete: " + err.Error()
			if view.State != "running" {
				view.State = "unknown"
			}
		}
	}
	namespace, err := os.Readlink(filepath.Join(root, "self", "ns", "net"))
	if err != nil {
		incomplete(err)
		return views
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		incomplete(err)
		return views
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			incomplete(err)
			break
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		netns, err := os.Readlink(filepath.Join(dir, "ns", "net"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			incomplete(err)
			continue
		}
		if netns != namespace {
			continue
		}
		executable, err := os.Readlink(filepath.Join(dir, "exe"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			incomplete(err)
			continue
		}
		executable = strings.TrimSuffix(executable, " (deleted)")
		id := owners[executable]
		if id == "" {
			continue
		}
		arguments, err := os.ReadFile(filepath.Join(dir, "cmdline"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			incomplete(err)
			continue
		}
		if len(arguments) == 0 || coreHelperInvocation(arguments) {
			continue
		}
		stat, err := os.ReadFile(filepath.Join(dir, "stat"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			incomplete(err)
			continue
		}
		end := strings.LastIndexByte(string(stat), ')')
		fields := strings.Fields(string(stat)[end+1:])
		if end < 0 || len(fields) < 20 {
			incomplete(fmt.Errorf("invalid process status for PID %d", pid))
			continue
		}
		state := "running"
		switch fields[0] {
		case "Z", "X", "x":
			continue
		case "T", "t":
			state = "stopped"
		case "R", "S", "D", "I":
		default:
			incomplete(fmt.Errorf("unknown process state for PID %d", pid))
			continue
		}
		currentExe, exeErr := os.Readlink(filepath.Join(dir, "exe"))
		currentNet, netErr := os.Readlink(filepath.Join(dir, "ns", "net"))
		if errors.Is(exeErr, os.ErrNotExist) || errors.Is(netErr, os.ErrNotExist) {
			continue
		}
		if err := errors.Join(exeErr, netErr); err != nil {
			incomplete(err)
			continue
		}
		if strings.TrimSuffix(currentExe, " (deleted)") != executable || currentNet != namespace {
			continue
		}
		view := views[id]
		view.Processes = append(view.Processes, CoreHostProcessView{PID: pid, Executable: executable, State: state})
		if state == "running" {
			view.State = state
		}
	}
	for _, view := range views {
		sort.Slice(view.Processes, func(i, j int) bool { return view.Processes[i].PID < view.Processes[j].PID })
	}
	return views
}

// Version and configuration probes can overlap a status poll; they are not daemons.
func coreHelperInvocation(command []byte) bool {
	for _, argument := range strings.Split(string(command), "\x00")[1:] {
		switch argument {
		case "version", "--version", "-version", "-v", "help", "--help", "-help", "-h", "check", "-test", "--test", "--test-config", "--test-crypto", "--genkey", "uuid", "x25519":
			return true
		}
		if strings.HasPrefix(argument, "--show-") {
			return true
		}
	}
	return false
}
