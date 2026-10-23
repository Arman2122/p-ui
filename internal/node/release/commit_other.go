//go:build !linux

package release

import "os"

func commitStage(source, destination string) error { return os.Rename(source, destination) }

func syncDirectory(_ string) error { return nil }
