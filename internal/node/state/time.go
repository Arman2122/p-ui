package state

import (
	"bytes"

	bolt "go.etcd.io/bbolt"
)

func (s *Store) ReadClock() ([]byte, error) {
	var result []byte
	err := s.db.View(func(tx *bolt.Tx) error {
		result = bytes.Clone(tx.Bucket(metaBucket).Get([]byte("trusted-clock")))
		return nil
	})
	return result, err
}

func (s *Store) WriteClock(data []byte) error {
	if len(data) == 0 || len(data) > 1024 {
		return ErrIdentity
	}
	return s.db.Update(func(tx *bolt.Tx) error { return tx.Bucket(metaBucket).Put([]byte("trusted-clock"), data) })
}
