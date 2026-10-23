package sshruntime

import (
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/Arman2122/p-ui/internal/cores/internal/ssh"
	"github.com/Arman2122/p-ui/internal/node/privatefile"
	"github.com/Arman2122/p-ui/internal/node/state"
)

func (s *Runtime) localHostKeys() (string, error) {
	if s.store == nil || !filepath.IsAbs(s.directory) {
		return "", errors.New("SSH host keys require private runtime state")
	}
	previous, err := s.store.MaterialDigest("ssh-host-keys")
	if err != nil {
		return "", err
	}
	path := filepath.Join(s.directory, "host-keys.pem")
	file, err := privatefile.Open(path, os.O_RDONLY, 0)
	if errors.Is(err, os.ErrNotExist) && previous == nil {
		key, err := ssh.NewNodeHostKeys()
		if err != nil {
			return "", err
		}
		file, err := os.CreateTemp(s.directory, ".host-keys-*")
		if err != nil {
			return "", err
		}
		temporary := file.Name()
		defer func() { _ = file.Close(); _ = os.Remove(temporary) }()
		if _, err := io.WriteString(file, key); err != nil {
			return "", err
		}
		if err := file.Sync(); err != nil {
			return "", err
		}
		if err := file.Close(); err != nil {
			return "", err
		}
		if err := privatefile.RenameNew(temporary, path); err != nil && !errors.Is(err, os.ErrExist) {
			return "", err
		}
		return s.localHostKeys()
	}
	if err != nil {
		return "", errors.Join(state.ErrIdentity, err)
	}
	defer file.Close()
	key, err := io.ReadAll(io.LimitReader(file, (32<<10)+1))
	if err != nil {
		return "", err
	}
	if len(key) > 32<<10 {
		return "", state.ErrIdentity
	}
	if err := ssh.ValidateNodeHostKeys(string(key)); err != nil {
		return "", err
	}
	if err := privatefile.SyncDirectory(s.directory); err != nil {
		return "", err
	}
	digest := sha256.Sum256(key)
	if err := s.store.BindMaterial("ssh-host-keys", digest[:]); err != nil {
		return "", err
	}
	return string(key), nil
}
