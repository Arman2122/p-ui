// Package sys provides system utilities for monitoring network connections and CPU usage.
package sys

import (
	_ "unsafe"
)

//go:linkname HostProc github.com/shirou/gopsutil/v4/internal/common.HostProc
func HostProc(combineWith ...string) string
