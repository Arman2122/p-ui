//go:build !linux

package privatefile

import "os"

func Open(path string, flags int, mode os.FileMode) (*os.File, error) {
	return os.OpenFile(path, flags, mode)
}
