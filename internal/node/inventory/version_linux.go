//go:build linux

package inventory

// /proc/self/exe identifies the running inode even after its release path is replaced.
func executablePath() (string, error) { return "/proc/self/exe", nil }
