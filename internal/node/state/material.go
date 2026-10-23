package state

import (
	"bytes"
	"crypto/sha256"
	"regexp"

	bolt "go.etcd.io/bbolt"
)

var materialName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,95}$`)

func (s *Store) MaterialDigest(name string) ([]byte, error) {
	if !materialName.MatchString(name) {
		return nil, ErrIdentity
	}
	var digest []byte
	err := s.db.View(func(tx *bolt.Tx) error {
		digest = bytes.Clone(tx.Bucket(metaBucket).Get([]byte("material:" + name)))
		if digest != nil && len(digest) != sha256.Size {
			return ErrIdentity
		}
		return nil
	})
	return digest, err
}

func (s *Store) BindMaterial(name string, digest []byte) error {
	if !materialName.MatchString(name) || len(digest) != sha256.Size {
		return ErrIdentity
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(metaBucket)
		key := []byte("material:" + name)
		if previous := bucket.Get(key); previous != nil && !bytes.Equal(previous, digest) {
			return ErrIdentity
		}
		return bucket.Put(key, digest)
	})
}
