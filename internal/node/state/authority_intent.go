package state

import (
	"bytes"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/limits"
)

var (
	authorityIntentBucket = []byte("authority-intents")
	authorityClientBucket = []byte("authority-intent-clients")
)

func (s *Store) validateAuthorityScope(tx *bolt.Tx, batch *nodev1.AuthorityUpdateBatch) error {
	meta := tx.Bucket(metaBucket)
	if batch.NodeId != s.identity.NodeID || batch.InstallationId != s.identity.InstallationID || batch.StateJournalId != s.journalID || !bytes.Equal(meta.Get(agentJournalKey), []byte(batch.AgentStateJournalId)) || !bytes.Equal(meta.Get([]byte("runtime-journal:"+batch.RuntimeId)), []byte(batch.CreditJournalId)) {
		return ErrIdentity
	}
	return nil
}

func readAuthorityIntent(encoded []byte) (*nodev1.AuthorityUpdateBatch, error) {
	batch := new(nodev1.AuthorityUpdateBatch)
	if len(encoded) > 8192 || proto.Unmarshal(encoded, batch) != nil || authority.ValidateAuthorityBatch(batch, false) != nil || len(batch.Updates) != 1 {
		return nil, ErrRevision
	}
	return batch, nil
}

func sameAuthorityBase(a, b *nodev1.AuthorityUpdateBatch) bool {
	return a.BaseConfigurationRevision == b.BaseConfigurationRevision && bytes.Equal(a.BasePayloadSha256, b.BasePayloadSha256)
}

func validateIntentAdvance(old, next *nodev1.AuthorityUpdateBatch) error {
	previous, current := old.Updates[0], next.Updates[0]
	if !sameAuthorityBase(old, next) {
		return nil
	}
	if previous.AuthorityRevision == current.AuthorityRevision {
		if !proto.Equal(old, next) {
			return ErrRevision
		}
		return nil
	}
	if old.RuntimeId != next.RuntimeId || previous.GrantId != current.GrantId || previous.ClientId != current.ClientId || previous.TermId != current.TermId || previous.CredentialGeneration != current.CredentialGeneration || previous.PlacementId != current.PlacementId || previous.PlacementRevision != current.PlacementRevision || !bytes.Equal(previous.DescriptorSha256, current.DescriptorSha256) {
		return ErrIdentity
	}
	before, err := authority.AuthorityPolicy(old.BaseConfigurationRevision, previous)
	if err != nil {
		return err
	}
	after, err := authority.AuthorityPolicy(next.BaseConfigurationRevision, current)
	if err != nil {
		return err
	}
	if !limits.AuthorityAdvance(before, after) {
		return ErrRevision
	}
	return nil
}

func (s *Store) SaveAuthorityIntents(batch *nodev1.AuthorityUpdateBatch) error {
	if err := authority.ValidateAuthorityBatch(batch, false); err != nil {
		return err
	}
	header := proto.Clone(batch).(*nodev1.AuthorityUpdateBatch)
	header.Updates = nil
	return s.db.Update(func(tx *bolt.Tx) error {
		if err := s.validateAuthorityScope(tx, batch); err != nil {
			return err
		}
		meta := tx.Bucket(metaBucket)
		if number(meta, "desired") != batch.BaseConfigurationRevision || number(meta, "applied") != batch.BaseConfigurationRevision {
			return ErrRevision
		}
		deployment := new(nodev1.Deployment)
		if proto.Unmarshal(tx.Bucket(deployBucket).Get(key(batch.BaseConfigurationRevision)), deployment) != nil || !bytes.Equal(deployment.PayloadSha256, batch.BasePayloadSha256) {
			return ErrRevision
		}
		bucket, err := tx.CreateBucketIfNotExists(authorityIntentBucket)
		if err != nil {
			return err
		}
		clients, err := tx.CreateBucketIfNotExists(authorityClientBucket)
		if err != nil {
			return err
		}
		for _, update := range batch.Updates {
			own := proto.Clone(header).(*nodev1.AuthorityUpdateBatch)
			own.Updates = []*nodev1.AuthorityUpdate{proto.Clone(update).(*nodev1.AuthorityUpdate)}
			if previous := bucket.Get([]byte(update.GrantId)); previous != nil {
				old, err := readAuthorityIntent(previous)
				if err != nil {
					return err
				}
				if err := s.validateAuthorityScope(tx, old); err != nil {
					return err
				}
				if err := validateIntentAdvance(old, own); err != nil {
					return err
				}
			}
			if oldID := clients.Get([]byte(update.ClientId)); oldID != nil && !bytes.Equal(oldID, []byte(update.GrantId)) {
				old, err := readAuthorityIntent(bucket.Get(oldID))
				if err != nil {
					return err
				}
				if sameAuthorityBase(old, own) {
					return ErrIdentity
				}
			}
			encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(own)
			if err != nil {
				return err
			}
			if len(encoded) > 8192 {
				return ErrRevision
			}
			if err := bucket.Put([]byte(update.GrantId), encoded); err != nil {
				return err
			}
			if err := clients.Put([]byte(update.ClientId), []byte(update.GrantId)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) AuthorityIntents(revision uint64, digest []byte) ([]*nodev1.AuthorityUpdateBatch, error) {
	var result []*nodev1.AuthorityUpdateBatch
	err := s.db.View(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(authorityIntentBucket)
		if bucket == nil {
			return nil
		}
		return bucket.ForEach(func(_ []byte, encoded []byte) error {
			intent, err := readAuthorityIntent(encoded)
			if err != nil {
				return err
			}
			if intent.BaseConfigurationRevision != revision || !bytes.Equal(intent.BasePayloadSha256, digest) {
				return nil
			}
			if err := s.validateAuthorityScope(tx, intent); err != nil {
				return err
			}
			if len(result) >= 1_000_000 {
				return ErrRevision
			}
			result = append(result, intent)
			return nil
		})
	})
	return result, err
}

func (s *Store) PruneAuthorityIntents(revision uint64, digest []byte) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		if number(tx.Bucket(metaBucket), "applied") != revision || number(tx.Bucket(metaBucket), "desired") != revision {
			return ErrRevision
		}
		bucket := tx.Bucket(authorityIntentBucket)
		if bucket == nil {
			return nil
		}
		clients := tx.Bucket(authorityClientBucket)
		cursor := bucket.Cursor()
		for key, encoded := cursor.First(); key != nil; key, encoded = cursor.Next() {
			intent, err := readAuthorityIntent(encoded)
			if err != nil {
				return err
			}
			if intent.BaseConfigurationRevision == revision && bytes.Equal(intent.BasePayloadSha256, digest) {
				continue
			}
			if clients != nil && bytes.Equal(clients.Get([]byte(intent.Updates[0].ClientId)), key) {
				if err := clients.Delete([]byte(intent.Updates[0].ClientId)); err != nil {
					return err
				}
			}
			if err := cursor.Delete(); err != nil {
				return err
			}
		}
		return nil
	})
}
