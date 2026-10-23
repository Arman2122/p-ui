package privatefile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPrivateJournalRejectsExposedAndLinkedFiles(t *testing.T) {
	for _, kind := range []string{"private", "readable", "hardlink", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			original := filepath.Join(dir, "original")
			if err := os.WriteFile(original, []byte("unchanged"), 0o600); err != nil {
				t.Fatal(err)
			}
			path := original
			switch kind {
			case "readable":
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				path = filepath.Join(dir, "link")
				if err := os.Link(original, path); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				path = filepath.Join(dir, "link")
				if err := os.Symlink(original, path); err != nil {
					t.Fatal(err)
				}
			}
			file, err := Open(path, os.O_RDWR, 0o600)
			if file != nil {
				_ = file.Close()
			}
			switch kind {
			case "private":
				if err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if !errors.Is(err, unix.ELOOP) {
					t.Fatalf("symlink error=%v", err)
				}
			default:
				if !errors.Is(err, ErrPermissions) {
					t.Fatalf("exposed journal error=%v", err)
				}
			}
			data, err := os.ReadFile(original)
			if err != nil || string(data) != "unchanged" {
				t.Fatalf("journal contents changed: %q,%v", data, err)
			}
		})
	}
}
