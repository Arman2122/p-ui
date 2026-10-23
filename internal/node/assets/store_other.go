//go:build !linux

package assets

import "github.com/Arman2122/p-ui/internal/node/configuration"

type Store struct{}

func Open(string) (*Store, error) { return nil, ErrFilesystem }

func (*Store) Stage([]configuration.FileAsset) (map[string]string, error) {
	return nil, ErrFilesystem
}

func (*Store) Prune([]string, int) (int, error) { return 0, ErrFilesystem }

func (*Store) Close() error { return nil }
