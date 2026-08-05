//go:build !linux

package panel

import (
	"os"
	"os/exec"
)

/*
Unreachable off Linux: the panel and its self-update run on Linux only. These
exist so the package compiles on a developer machine, which the "_unix" suffix
never achieved — Go has no such build constraint, so panel_linux.go used to be
compiled everywhere and took this package, internal/web and main down with it.

See panel_linux.go for the real implementations and why processAlive answers
the way it does.
*/

func setDetachedProcess(*exec.Cmd) {}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	_, err := os.FindProcess(pid)
	return err == nil
}
