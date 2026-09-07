// Package mtproto manages mtg-proxy (github.com/Arman2122/mtg-proxy) sidecar
// processes that serve MTProto FakeTLS proxies. Xray-core has no mtproto
// protocol, so mtproto inbounds are run as standalone mtg processes — one
// process per inbound, each serving every active client's secret through the
// mtg-proxy [secrets] section — entirely outside the Xray config and lifecycle.
// A client edit is hot-applied via the fork's POST /reload endpoint so live
// connections survive; the manager falls back to a restart on older binaries.
package mtproto

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Arman2122/p-ui/internal/config"
	"github.com/Arman2122/p-ui/internal/logger"
	"github.com/Arman2122/p-ui/internal/supervisor"
	"github.com/Arman2122/p-ui/internal/util/proc"
)

// GetBinaryName returns the mtg binary filename for the current arch, matching
// the naming scheme used for the Xray binary (e.g. "mtg-linux-amd64").
func GetBinaryName() string {
	return fmt.Sprintf("mtg-%s-%s", runtime.GOOS, runtime.GOARCH)
}

// GetBinaryPath returns the full path to the mtg binary, alongside the Xray binary.
func GetBinaryPath() string {
	return config.GetBinFolderPath() + "/" + GetBinaryName()
}

// CheckBinary reports whether the mtg binary is present, executable and new
// enough. A miss disables the core at preflight instead of failing each inbound
// every reconcile; an old binary would take every limit the panel writes
// silently, so it is refused by name.
func CheckBinary() error {
	path := GetBinaryPath()
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("mtproto: mtg binary %s: %w", path, err)
	}
	if info.IsDir() || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("mtproto: %s is not an executable file", path)
	}
	version, err := BinaryVersion(context.Background(), path)
	if err != nil {
		return err
	}
	return CheckVersion(version)
}

// MinimumVersion is the first mtg-proxy that answers everything the panel
// drives: per-direction speed, per-address activity and the kick.
const MinimumVersion = "1.9.0"

// CheckVersion refuses a release older than MinimumVersion. A version that is
// not a release number (a source build says "dev") is let through: it is newer
// than any release by construction, and refusing it would make every VM run
// impossible.
func CheckVersion(version string) error {
	have, ok := parseRelease(version)
	if !ok {
		return nil
	}
	want, _ := parseRelease(MinimumVersion)
	if slices.Compare(have[:], want[:]) < 0 {
		return fmt.Errorf("mtproto: mtg-proxy %s is older than %s, which the panel's speed and device limits need; install a newer release", version, MinimumVersion)
	}
	return nil
}

// parseRelease reads "1.9.0" or "v1.9.0"; anything else is not a release.
func parseRelease(version string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(strings.TrimSpace(version), "v"), ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// BinaryVersion is what the binary says of itself: "1.3.0" from
// "1.3.0 (go1.26.6: …)", or "dev" for a source build.
func BinaryVersion(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("mtproto: %s --version: %w", filepath.Base(path), err)
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "", errors.New("mtproto: --version answered nothing")
	}
	return fields[0], nil
}

func configDir() string {
	return config.GetBinFolderPath() + "/mtproto"
}

func configPathForID(id int) string {
	return fmt.Sprintf("%s/mtg-%d.toml", configDir(), id)
}

var (
	gracefulStopTimeout = 5 * time.Second
	forceStopTimeout    = 2 * time.Second
)

// procLogWriter consumes the mtg child process's stdout/stderr. It splits the
// stream into lines, forwards each one to the p-ui log — so mtg's own messages,
// including why it cannot reach Telegram, become visible in the panel log viewer
// and journald — and remembers the most recent line for GetResult.
type procLogWriter struct {
	mu       sync.Mutex
	label    string
	buf      string
	lastLine string
	// ring keeps the last lines for TailLog; the manager hands one in that
	// outlives this process, so a crash's last words survive the restart.
	ring *supervisor.LineRing
	// ringPrefix marks the lines of a second process sharing the ring.
	ringPrefix string
}

func (w *procLogWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf += string(p)
	for {
		i := strings.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := w.buf[:i]
		w.buf = w.buf[i+1:]
		w.emitLocked(line)
	}
	return len(p), nil
}

// Flush emits any buffered partial line; called once the process exits so a
// final un-terminated error line is not lost.
func (w *procLogWriter) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.buf != "" {
		line := w.buf
		w.buf = ""
		w.emitLocked(line)
	}
}

func (w *procLogWriter) emitLocked(line string) {
	trimmed := strings.TrimSpace(strings.TrimRight(line, "\r"))
	if trimmed == "" {
		return
	}
	w.lastLine = trimmed
	if w.ring != nil {
		_, _ = w.ring.Write([]byte(w.ringPrefix + trimmed + "\n"))
	}
	logger.Infof("mtproto: mtg %s | %s", w.label, trimmed)
}

func (w *procLogWriter) LastLine() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastLine
}

// Process wraps a single mtg process invocation for one mtproto inbound.
type Process struct {
	mu              sync.RWMutex
	cmd             *exec.Cmd
	done            chan struct{}
	configPath      string
	logWriter       *procLogWriter
	exitErr         error
	started         time.Time
	inboundID       int
	intentionalStop atomic.Bool
	// args is the subcommand and its arguments: "run" for the proxy, "web-relay"
	// for the relay beside it, both reading the same file.
	args []string
}

func newProcess(configPath, label string, inboundID int) *Process {
	return &Process{
		configPath: configPath,
		logWriter:  &procLogWriter{label: label},
		inboundID:  inboundID,
		args:       []string{"run", configPath},
	}
}

// newRelayProcess is the WEB relay for the same file; its lines carry a prefix
// in the shared ring so an operator can tell the two processes apart.
func newRelayProcess(configPath, label string, inboundID int) *Process {
	return &Process{
		configPath: configPath,
		logWriter:  &procLogWriter{label: label, ringPrefix: "web-relay | "},
		inboundID:  inboundID,
		args:       []string{"web-relay", configPath},
	}
}

// IsRunning reports whether the mtg process is currently running.
func (p *Process) IsRunning() bool {
	p.mu.RLock()
	cmd, done := p.cmd, p.done
	p.mu.RUnlock()
	if cmd == nil || cmd.Process == nil {
		return false
	}
	if done != nil {
		select {
		case <-done:
			return false
		default:
		}
	}
	return true
}

// GetResult returns the last log line or the exit error from the mtg process.
func (p *Process) GetResult() string {
	if line := p.logWriter.LastLine(); line != "" {
		return line
	}
	p.mu.RLock()
	exitErr := p.exitErr
	p.mu.RUnlock()
	if exitErr != nil {
		return exitErr.Error()
	}
	return ""
}

// Start launches the mtg process against its generated config file.
func (p *Process) Start() error {
	if p.IsRunning() {
		return errors.New("mtg is already running")
	}
	cmd := exec.CommandContext(context.Background(), GetBinaryPath(), p.args...)
	cmd.Stdout = p.logWriter
	cmd.Stderr = p.logWriter
	done := make(chan struct{})
	p.mu.Lock()
	p.cmd = cmd
	p.done = done
	p.exitErr = nil
	p.started = time.Now()
	p.mu.Unlock()
	p.intentionalStop.Store(false)
	if err := cmd.Start(); err != nil {
		close(done)
		p.mu.Lock()
		p.cmd = nil
		p.mu.Unlock()
		return err
	}
	go p.wait(cmd, done)
	return nil
}

func (p *Process) wait(cmd *exec.Cmd, done chan struct{}) {
	defer close(done)
	err := cmd.Wait()
	p.logWriter.Flush()
	if err == nil || p.intentionalStop.Load() {
		return
	}
	logger.Errorf("mtproto: mtg process exited: %v", err)
	p.setExitErr(err)
}

func (p *Process) setExitErr(err error) {
	p.mu.Lock()
	p.exitErr = err
	p.mu.Unlock()
}

// Stop terminates the running mtg process gracefully, falling back to a kill.
func (p *Process) Stop() error {
	if !p.IsRunning() {
		return errors.New("mtg is not running")
	}
	p.intentionalStop.Store(true)
	p.mu.RLock()
	cmd, done := p.cmd, p.done
	p.mu.RUnlock()
	if cmd == nil || cmd.Process == nil {
		return errors.New("mtg is not running")
	}

	return proc.Stop(cmd, done, gracefulStopTimeout, forceStopTimeout, "mtg process")
}

// keepLines routes the process's output into a ring the caller owns.
func (p *Process) keepLines(ring *supervisor.LineRing) { p.logWriter.ring = ring }

// Signal delivers sig to the running process; nothing to a stopped one.
func (p *Process) Signal(sig os.Signal) error {
	p.mu.RLock()
	cmd := p.cmd
	p.mu.RUnlock()
	if cmd == nil || cmd.Process == nil || !p.IsRunning() {
		return nil
	}
	return cmd.Process.Signal(sig)
}

// PID is the child's, or 0 while none runs.
func (p *Process) PID() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.cmd == nil || p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// Started is when this life of the process began; zero before the first Start.
func (p *Process) Started() time.Time {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.started
}

// Crashed reports an exit the panel did not ask for.
func (p *Process) Crashed() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.exitErr != nil
}
