package service

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/Arman2122/p-ui/internal/core"
)

func hostProcRoot(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("procfs observation is Linux-only")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "self", "ns"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("net:[10]", filepath.Join(root, "self", "ns", "net")); err != nil {
		t.Fatal(err)
	}
	return root
}

func hostProc(t *testing.T, root string, pid int, executable, namespace, state string) {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(pid))
	if err := os.MkdirAll(filepath.Join(dir, "ns"), 0o700); err != nil {
		t.Fatal(err)
	}
	for path, target := range map[string]string{"exe": executable, filepath.Join("ns", "net"): namespace} {
		if err := os.Symlink(target, filepath.Join(dir, path)); err != nil {
			t.Fatal(err)
		}
	}
	stat := strconv.Itoa(pid) + " (process ) title) " + state + strings.Repeat(" 0", 19)
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(stat), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(executable+"\x00run\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCoreHostRuntimeReadsActualProcessState(t *testing.T) {
	root := hostProcRoot(t)
	hostProc(t, root, 104, "/usr/bin/proxy", "net:[10]", "T")
	hostProc(t, root, 102, "/usr/bin/proxy (deleted)", "net:[10]", "S")
	hostProc(t, root, 103, "/usr/bin/proxy", "net:[10]", "Z")
	hostProc(t, root, 105, "/usr/bin/proxy", "net:[20]", "S")
	hostProc(t, root, 106, "/other/proxy", "net:[10]", "S")
	hostProc(t, root, 107, "/usr/bin/paused", "net:[10]", "t")
	hostProc(t, root, 108, "/usr/bin/proxy", "net:[10]", "R")
	if err := os.WriteFile(filepath.Join(root, "108", "cmdline"), []byte("proxy\x00version\x00"), 0o600); err != nil {
		t.Fatal(err)
	}
	views := readCoreHostRuntimes(context.Background(), root, map[string][]string{
		"daemon": {"/usr/bin/proxy"}, "paused": {"/usr/bin/paused"}, "unused": {"/usr/bin/unused"},
	})
	daemon := views["daemon"]
	if daemon.State != "running" || daemon.Error != "" || len(daemon.Processes) != 2 {
		t.Fatalf("daemon = %+v, want running with exactly two same-namespace matching processes", daemon)
	}
	if daemon.Processes[0].PID != 102 || daemon.Processes[0].Executable != "/usr/bin/proxy" || daemon.Processes[1].State != "stopped" {
		t.Fatalf("processes = %+v, want sorted live/deleted-executable and suspended observations", daemon.Processes)
	}
	if views["paused"].State != "stopped" || views["unused"].State != "stopped" || views["unused"].Processes == nil {
		t.Fatalf("paused=%+v unused=%+v, want stopped with a non-nil empty process list", views["paused"], views["unused"])
	}
}

func TestCoreHostRuntimeUnknownWhenObservationFails(t *testing.T) {
	for _, reason := range []string{"namespace", "malformed", "cancelled"} {
		t.Run(reason, func(t *testing.T) {
			root := hostProcRoot(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			hostProc(t, root, 101, "/usr/bin/proxy", "net:[10]", "R")
			switch reason {
			case "namespace":
				if err := os.Remove(filepath.Join(root, "self", "ns", "net")); err != nil {
					t.Fatal(err)
				}
			case "malformed":
				if err := os.WriteFile(filepath.Join(root, "101", "stat"), []byte("invalid"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "cancelled":
				cancel()
			}
			view := readCoreHostRuntimes(ctx, root, map[string][]string{"daemon": {"/usr/bin/proxy"}})["daemon"]
			if view.State != "unknown" || !strings.HasPrefix(view.Error, "Host process observation incomplete: ") || len(view.Processes) != 0 {
				t.Fatalf("view = %+v, want unknown with an explicit observation error", view)
			}
		})
	}
}

func TestCoreHostRuntimeObservesRealProcfs(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("procfs observation is Linux-only")
	}
	executable, err := os.Readlink("/proc/self/exe")
	if err != nil {
		t.Fatal(err)
	}
	view := readCoreHostRuntimes(context.Background(), "/proc", map[string][]string{"test": {executable}})["test"]
	for _, process := range view.Processes {
		if process.PID == os.Getpid() && process.State == "running" && view.State == "running" {
			return
		}
	}
	t.Fatalf("view = %+v, want the actual running test executable", view)
}

func TestCoreStatusViewsIncludesHostProcesses(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("procfs observation is Linux-only")
	}
	registry := core.NewRegistry()
	adapter := &healthCore{statusBase: statusBase{id: "openvpn", kinds: []core.Kind{"openvpn"}}}
	if err := registry.Register(adapter); err != nil {
		t.Fatal(err)
	}
	views := coreStatusViews(context.Background(), registry)
	if len(views) != 1 || views[0].HostRuntime == nil || views[0].HostRuntime.Processes == nil {
		t.Fatalf("views = %+v, want host process observations alongside managed health", views)
	}
	if views[0].State != "idle" || len(views[0].Instances) != 0 {
		t.Fatalf("view = %+v, host observation must not fabricate managed instances", views[0])
	}
}
