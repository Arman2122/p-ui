//go:build !linux

package inventory

import "os"

// Non-Linux builds support portable tests; production worker startup requires Linux.
func executablePath() (string, error) { return os.Executable() }
