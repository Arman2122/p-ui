package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
)

func loadAdditions(patched map[string][]byte, additions []patchAddition, module, directory string) error {
	for _, addition := range additions {
		if !filepath.IsLocal(addition.Path) || !filepath.IsLocal(addition.Source) || filepath.Ext(addition.Path) != ".go" || addition.Reason == "" {
			return fmt.Errorf("invalid core facade path")
		}
		if _, exists := patched[addition.Path]; exists {
			return fmt.Errorf("duplicate core facade path")
		}
		if _, err := os.Lstat(filepath.Join(module, filepath.FromSlash(addition.Path))); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("core facade would replace an upstream file: %s", addition.Path)
		}
		data, err := os.ReadFile(filepath.Join(directory, filepath.FromSlash(addition.Source)))
		if err != nil {
			return err
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != addition.SHA256 {
			return fmt.Errorf("core facade checksum differs: %s", addition.Path)
		}
		data, err = format.Source(data)
		if err != nil {
			return err
		}
		patched[addition.Path] = data
	}
	return nil
}
