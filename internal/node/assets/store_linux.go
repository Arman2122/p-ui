package assets

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"golang.org/x/sys/unix"

	"github.com/Arman2122/p-ui/internal/node/configuration"
)

type Store struct {
	mu     sync.Mutex
	path   string
	root   *os.File
	dir    *os.File
	closed bool
}

func Open(stateDir string) (*Store, error) {
	if !filepath.IsAbs(stateDir) || filepath.Clean(stateDir) != stateDir {
		return nil, ErrFilesystem
	}
	root, err := openDirectory(unix.AT_FDCWD, stateDir, false)
	if err != nil {
		return nil, err
	}
	s := &Store{path: filepath.Join(stateDir, "assets"), root: root}
	fail := func(err error) (*Store, error) { _ = s.Close(); return nil, err }
	if err := unix.Mkdirat(int(root.Fd()), "assets", 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
		return fail(err)
	}
	s.dir, err = openDirectory(int(root.Fd()), "assets", true)
	if err != nil {
		return fail(err)
	}
	if err := errors.Join(s.dir.Sync(), root.Sync()); err != nil {
		return fail(err)
	}
	return s, nil
}

func openDirectory(parent int, name string, beneath bool) (*os.File, error) {
	resolve := uint64(unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS)
	if beneath {
		resolve |= unix.RESOLVE_BENEATH
	}
	fd, err := unix.Openat2(parent, name, &unix.OpenHow{Flags: unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC, Resolve: resolve})
	if err != nil {
		return nil, errors.Join(ErrFilesystem, err)
	}
	file := os.NewFile(uintptr(fd), name)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Mode&0o7777 != 0o700 || stat.Uid != uint32(os.Geteuid()) {
		_ = file.Close()
		return nil, errors.Join(ErrFilesystem, err)
	}
	return file, nil
}

func sameDirectory(first, second *os.File) bool {
	var left, right unix.Stat_t
	return unix.Fstat(int(first.Fd()), &left) == nil && unix.Fstat(int(second.Fd()), &right) == nil && left.Dev == right.Dev && left.Ino == right.Ino
}

func (s *Store) checkAnchor() error {
	if s.closed {
		return ErrClosed
	}
	root, err := openDirectory(unix.AT_FDCWD, filepath.Dir(s.path), false)
	if err != nil {
		return err
	}
	defer root.Close()
	dir, err := openDirectory(int(root.Fd()), "assets", true)
	if err != nil {
		return err
	}
	defer dir.Close()
	if !sameDirectory(root, s.root) || !sameDirectory(dir, s.dir) {
		return ErrFilesystem
	}
	return nil
}

func (s *Store) openRawFile(name string, flags int, mode uint64) (*os.File, error) {
	fd, err := unix.Openat2(int(s.dir.Fd()), name, &unix.OpenHow{
		Flags: uint64(flags | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK), Mode: mode,
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	})
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

func checkFile(file *os.File) error {
	var stat unix.Stat_t
	if err := unix.Fstat(int(file.Fd()), &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o7777 != 0o600 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		return errors.Join(ErrFilesystem, err)
	}
	return nil
}

func (s *Store) openFile(name string, flags int, mode uint64) (*os.File, error) {
	file, err := s.openRawFile(name, flags, mode)
	if err != nil {
		return nil, err
	}
	if err := checkFile(file); err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func (s *Store) verify(name string) error {
	file, err := s.openFile(name, unix.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	count, err := io.Copy(hash, io.LimitReader(file, configuration.MaxFileAssetBytes+1))
	if err != nil {
		return err
	}
	if count == 0 || count > configuration.MaxFileAssetBytes || hex.EncodeToString(hash.Sum(nil)) != name {
		return ErrContent
	}
	return nil
}

// Stage publishes immutable candidates; callers validate them before persisting desired state.
func (s *Store) Stage(input []configuration.FileAsset) (map[string]string, error) {
	if len(input) > configuration.MaxRuntimeFileAssets {
		return nil, configuration.ErrConfiguration
	}
	assets := slices.Clone(input)
	total := 0
	for i := range assets {
		total += len(assets[i].Data)
		if total > configuration.MaxBundleFileBytes || len(assets[i].Data) > configuration.MaxFileAssetBytes {
			return nil, configuration.ErrConfiguration
		}
		assets[i].Data = bytes.Clone(assets[i].Data)
	}
	if err := configuration.ValidateFileAssets(assets); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkAnchor(); err != nil {
		return nil, err
	}
	paths := make(map[string]string, len(assets))
	for _, asset := range assets {
		if err := s.stage(asset); err != nil {
			return nil, err
		}
		paths[configuration.AssetReference(asset.ID, asset.SHA256)] = filepath.Join(s.path, asset.SHA256)
	}
	if err := errors.Join(s.dir.Sync(), s.checkAnchor()); err != nil {
		return nil, err
	}
	return paths, nil
}

func (s *Store) stage(asset configuration.FileAsset) (result error) {
	if err := s.verify(asset.SHA256); err == nil {
		return nil
	} else if !errors.Is(err, unix.ENOENT) {
		return err
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	name := ".stage-" + hex.EncodeToString(nonce[:])
	file, err := s.openRawFile(name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
		if err := unix.Unlinkat(int(s.dir.Fd()), name, 0); err != nil && !errors.Is(err, unix.ENOENT) {
			result = errors.Join(result, err)
		}
	}()
	if err := checkFile(file); err != nil {
		return err
	}
	count, writeErr := file.Write(asset.Data)
	if writeErr == nil && count != len(asset.Data) {
		writeErr = io.ErrShortWrite
	}
	if err := errors.Join(writeErr, file.Sync(), file.Close()); err != nil {
		return err
	}
	if err := unix.Renameat2(int(s.dir.Fd()), name, int(s.dir.Fd()), asset.SHA256, unix.RENAME_NOREPLACE); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return s.verify(asset.SHA256)
		}
		return err
	}
	return s.verify(asset.SHA256)
}

// Prune requires every applied and desired digest; callers serialize it with deployment changes.
func (s *Store) Prune(keep []string, limit int) (int, error) {
	if limit < 1 || limit > configuration.MaxRuntimeFileAssets || len(keep) > 2*configuration.MaxRuntimeFileAssets {
		return 0, ErrBounds
	}
	retained := make(map[string]bool, len(keep))
	for _, digest := range keep {
		if !validDigest(digest) {
			return 0, ErrContent
		}
		retained[digest] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkAnchor(); err != nil {
		return 0, err
	}
	if _, err := s.dir.Seek(0, io.SeekStart); err != nil {
		return 0, err
	}
	names, err := s.dir.Readdirnames(4097)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}
	if len(names) > 4096 {
		return 0, ErrBounds
	}
	slices.Sort(names)
	var remove []string
	for _, name := range names {
		if retained[name] {
			continue
		}
		if validDigest(name) {
			if err := s.verify(name); err != nil {
				return 0, err
			}
		} else if nonce, ok := strings.CutPrefix(name, ".stage-"); ok && len(nonce) == 32 {
			decoded, err := hex.DecodeString(nonce)
			if err != nil || hex.EncodeToString(decoded) != nonce {
				return 0, ErrFilesystem
			}
			file, err := s.openFile(name, unix.O_RDONLY, 0)
			if err != nil {
				return 0, err
			}
			if err := file.Close(); err != nil {
				return 0, err
			}
		} else {
			return 0, ErrFilesystem
		}
		remove = append(remove, name)
		if len(remove) == limit {
			break
		}
	}
	removed := 0
	for _, name := range remove {
		if err := unix.Unlinkat(int(s.dir.Fd()), name, 0); err != nil {
			return removed, errors.Join(err, s.dir.Sync())
		}
		removed++
	}
	return removed, errors.Join(s.dir.Sync(), s.checkAnchor())
}

func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	var directory, root error
	if s.dir != nil {
		directory = s.dir.Close()
	}
	if s.root != nil {
		root = s.root.Close()
	}
	return errors.Join(directory, root)
}
