package state

import (
	"bytes"
	"crypto/sha256"

	bolt "go.etcd.io/bbolt"
)

func (s *Store) AppliedBase() (uint64, []byte, error) {
	var revision uint64
	var digest []byte
	err := s.db.View(func(tx *bolt.Tx) error {
		meta := tx.Bucket(metaBucket)
		revision = number(meta, "applied")
		value := meta.Get([]byte("applied-digest"))
		if revision == 0 || revision != number(meta, "desired") || len(value) != sha256.Size {
			return ErrRevision
		}
		digest = bytes.Clone(value)
		return nil
	})
	return revision, digest, err
}
