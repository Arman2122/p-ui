package mtproto

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// killStrayMtgProcesses terminates orphaned mtg sidecars left over from a
// previous p-ui run and returns how many were killed.
//
// p-ui starts one mtg process per mtproto inbound outside its own lifecycle, and
// a child is not guaranteed to die with the panel (Linux has no kill-on-exit for
// child processes). A survivor keeps holding the inbound port with a now-stale
// secret, so new clients are silently domain-fronted to the FakeTLS domain
// instead of proxied to Telegram. p-ui is the sole owner of mtg, so any process
// matching our binary name at startup is an orphan and is safe to kill before we
// start our own.
//
// binaryPath is the configured mtg path (e.g. "bin/mtg-linux-amd64"), resolved
// to an absolute path and matched in full — see matchesBinary for why the base
// name alone is not safe.
func killStrayMtgProcesses(binaryPath string) int {
	want, err := filepath.Abs(binaryPath)
	if err != nil {
		return 0
	}
	if base := filepath.Base(want); base == "." || base == string(filepath.Separator) {
		return 0
	}
	self := os.Getpid()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return 0
	}
	killed := 0
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == self {
			continue
		}
		if !matchesBinary(procExePath(pid), cmdlineArgv0Path(pid), want) {
			continue
		}
		// os.Process.Kill is SIGKILL on Linux and, unlike syscall.Kill, compiles
		// off Linux — the arch guards and unit tests need that, the panel does not.
		proc, err := os.FindProcess(pid)
		if err != nil {
			continue
		}
		if err := proc.Kill(); err == nil {
			killed++
		}
	}
	return killed
}

/*
matchesBinary reports whether a process is running the binary at want.

Full path, never the base name. This function's answer becomes a SIGKILL, and
any other mtg on the host — a second install, or a test run from a source tree —
carries the same file name under a different bin folder. Matching by name killed
a live sidecar exactly that way.

Linux appends " (deleted)" to /proc/<pid>/exe once a running binary has been
replaced, which an update does routinely, so that suffix is stripped rather than
read as a different file. argv[0] stays as the fallback for an unreadable exe.
*/
func matchesBinary(exePath, argv0Path, want string) bool {
	for _, path := range [...]string{exePath, argv0Path} {
		if path != "" && strings.TrimSuffix(path, " (deleted)") == want {
			return true
		}
	}
	return false
}

// procExePath returns the target of /proc/<pid>/exe, or "" if unreadable.
func procExePath(pid int) string {
	exe, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return ""
	}
	return exe
}

// cmdlineArgv0Path returns argv[0] from /proc/<pid>/cmdline as an absolute path,
// resolving a relative argv[0] against that process's own working directory.
func cmdlineArgv0Path(pid int) string {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil || len(data) == 0 {
		return ""
	}
	argv0 := data
	if i := strings.IndexByte(string(data), 0); i >= 0 {
		argv0 = data[:i]
	}
	if len(argv0) == 0 {
		return ""
	}
	path := string(argv0)
	if filepath.IsAbs(path) {
		return path
	}
	cwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
	if err != nil {
		return ""
	}
	return filepath.Join(cwd, path)
}
