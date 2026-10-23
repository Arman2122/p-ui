package state

import (
	"bytes"

	bolt "go.etcd.io/bbolt"
)

var agentJournalKey = []byte("agent-state-journal")

func (s *Store) CheckAgentJournal(id string) error {
	if !validID(id) || id == s.journalID {
		return ErrIdentity
	}
	return s.db.View(func(tx *bolt.Tx) error {
		previous := tx.Bucket(metaBucket).Get(agentJournalKey)
		if previous != nil && !bytes.Equal(previous, []byte(id)) {
			return ErrIdentity
		}
		return nil
	})
}

func (s *Store) RequireAgentJournal(id string) error {
	if !validID(id) || id == s.journalID {
		return ErrIdentity
	}
	return s.db.View(func(tx *bolt.Tx) error {
		if !bytes.Equal(tx.Bucket(metaBucket).Get(agentJournalKey), []byte(id)) {
			return ErrIdentity
		}
		return nil
	})
}

func (s *Store) BindAgentJournal(id string) error {
	if !validID(id) || id == s.journalID {
		return ErrIdentity
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		meta := tx.Bucket(metaBucket)
		if number(meta, "desired") == 0 {
			return ErrRevision
		}
		if previous := meta.Get(agentJournalKey); previous != nil && !bytes.Equal(previous, []byte(id)) {
			return ErrIdentity
		}
		return meta.Put(agentJournalKey, []byte(id))
	})
}
