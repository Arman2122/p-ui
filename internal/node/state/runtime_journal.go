package state

import (
	"bytes"
	"regexp"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
)

var runtimeName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)

func (s *Store) BindRuntimeJournal(runtime, journalID string) error {
	if !runtimeName.MatchString(runtime) || !validID(journalID) {
		return ErrIdentity
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(metaBucket)
		key := []byte("runtime-journal:" + runtime)
		if previous := bucket.Get(key); previous != nil && !bytes.Equal(previous, []byte(journalID)) {
			return ErrIdentity
		}
		return bucket.Put(key, []byte(journalID))
	})
}

func (s *Store) BindRuntimeJournals(journals []*nodev1.RuntimeJournalIdentity) error {
	if len(journals) == 0 || len(journals) > 32 {
		return ErrIdentity
	}
	seen := make(map[string]bool, len(journals))
	for _, journal := range journals {
		if journal == nil || !runtimeName.MatchString(journal.RuntimeId) || seen[journal.RuntimeId] || !runtimeName.MatchString(journal.CoreId) || !validID(journal.StateJournalId) || !validID(journal.CreditJournalId) {
			return ErrIdentity
		}
		seen[journal.RuntimeId] = true
	}
	bound := true
	if err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(metaBucket)
		for _, journal := range journals {
			encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(journal)
			if err != nil {
				return err
			}
			if !bytes.Equal(bucket.Get([]byte("runtime-pair:"+journal.RuntimeId)), encoded) {
				bound = false
				break
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if bound {
		return nil
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(metaBucket)
		for _, journal := range journals {
			key := []byte("runtime-pair:" + journal.RuntimeId)
			encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(journal)
			if err != nil {
				return err
			}
			if previous := bucket.Get(key); previous != nil && !bytes.Equal(previous, encoded) {
				return ErrIdentity
			}
			if err := bucket.Put(key, encoded); err != nil {
				return err
			}
		}
		return nil
	})
}
