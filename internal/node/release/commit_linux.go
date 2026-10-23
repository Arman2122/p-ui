//go:build linux

package release

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func syncDirectory(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Sync()
}

func commitStage(source, destination string) error {
	if err := syncDirectory(source); err != nil {
		return err
	}
	if err := unix.Renameat2(unix.AT_FDCWD, source, unix.AT_FDCWD, destination, unix.RENAME_NOREPLACE); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(destination))
}
