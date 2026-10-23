//go:build !linux && !windows

package privatefile

import "errors"

func RenameNew(string, string) error {
	return errors.New("atomic private-file creation requires Linux")
}
func SyncDirectory(string) error { return nil }
