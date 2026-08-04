package xray

import (
	"fmt"
	"os"
	"sync"
)

/*
Ownership of the running Xray process.

This lived in internal/web/service, which put it out of reach of anything that is
not the web layer — and a protocol core may not import the web layer, by design
and by the guard in internal/arch. Moving it here changes no behaviour; it is what
lets the Xray core supervise its own daemon instead of the service layer doing it
on the core's behalf.

Panel policy stays in the service: whether the operator stopped Xray by hand, and
whether a setting change has queued a restart, are not facts about the process.
*/

// Manager owns the current Xray process and the last result reported for it.
type Manager struct {
	mu      sync.RWMutex
	process *Process
	result  string
}

var (
	managerOnce sync.Once
	manager     *Manager
)

// GetManager returns the process-wide Xray manager singleton.
func GetManager() *Manager {
	managerOnce.Do(func() { manager = &Manager{} })
	return manager
}

// Snapshot returns the current process and its stored result together, so a
// caller cannot pair one incarnation's process with another's result.
func (m *Manager) Snapshot() (*Process, string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.process, m.result
}

// Current returns the running process, or nil when Xray has never been started.
func (m *Manager) Current() *Process {
	process, _ := m.Snapshot()
	return process
}

// Replace installs a new process and clears the previous result.
func (m *Manager) Replace(process *Process) {
	m.mu.Lock()
	m.process = process
	m.result = ""
	m.mu.Unlock()
}

// StoreResult records why a process ended, but only while it is still the
// current one: a late result from a replaced process would otherwise be read as
// the state of the process that succeeded it.
func (m *Manager) StoreResult(process *Process, result string) {
	m.mu.Lock()
	if m.process == process && m.result == "" {
		m.result = result
	}
	m.mu.Unlock()
}

// CheckBinary reports whether the Xray binary is present and executable. A miss
// disables the core at preflight instead of failing each inbound every restart.
func CheckBinary() error {
	path := GetBinaryPath()
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("xray: binary %s: %w", path, err)
	}
	if info.IsDir() || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("xray: %s is not an executable file", path)
	}
	return nil
}
