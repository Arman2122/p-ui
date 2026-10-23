package state

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/compatibility"
	"github.com/Arman2122/p-ui/internal/node/privatefile"
)

var (
	ErrIdentity    = errors.New("node store identity mismatch")
	ErrRevision    = errors.New("stale or conflicting deployment")
	ErrDigest      = errors.New("deployment digest mismatch")
	ErrUsage       = errors.New("invalid usage checkpoint")
	ErrSpoolFull   = errors.New("durable usage spool is full")
	ErrStateFormat = errors.New("unsupported node state format; existing journals were preserved")
)

const (
	MaxDeploymentBytes = 32 << 20
	MaxBatchBytes      = 1 << 20
	MaxSpoolBytes      = 64 << 20
)

var (
	metaBucket     = []byte("meta")
	deployBucket   = []byte("deployments")
	usageBucket    = []byte("usage")
	metersBucket   = []byte("meters")
	countersBucket = []byte("counter_checkpoints")
	mutableBucket  = []byte("mutable_usage")
)

type Identity struct {
	NodeID         string
	InstallationID string
	ControlPlaneID string
}

type (
	Store struct {
		db        *bolt.DB
		identity  Identity
		journalID string
	}
	Cursors struct{ Desired, Applied, Authority, DurableUsage, AccountedUsage uint64 }
)

func validID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed != uuid.Nil && parsed.String() == id
}

func Open(path string, identity Identity) (*Store, error) {
	if !validID(identity.NodeID) || !validID(identity.InstallationID) || !validID(identity.ControlPlaneID) {
		return nil, ErrIdentity
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, ErrIdentity
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: time.Second, OpenFile: privatefile.Open})
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, identity: identity}
	encoded, err := json.Marshal(identity)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	err = db.Update(func(tx *bolt.Tx) error {
		meta := tx.Bucket(metaBucket)
		if meta == nil {
			if name, _ := tx.Cursor().First(); name != nil {
				return ErrStateFormat
			}
		} else if version := meta.Get([]byte("state-format")); len(version) != 8 || binary.BigEndian.Uint64(version) != uint64(compatibility.StateFormat) {
			return ErrStateFormat
		}
		for _, name := range [][]byte{metaBucket, deployBucket, usageBucket, metersBucket, countersBucket, mutableBucket} {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return err
			}
		}
		meta = tx.Bucket(metaBucket)
		if err := putNumber(meta, "state-format", uint64(compatibility.StateFormat)); err != nil {
			return err
		}
		if saved := meta.Get([]byte("identity")); saved != nil && !bytes.Equal(saved, encoded) {
			return ErrIdentity
		}
		if saved := meta.Get([]byte("journal-id")); saved != nil {
			s.journalID = string(saved)
			if !validID(s.journalID) {
				return ErrIdentity
			}
		} else {
			s.journalID = uuid.NewString()
			if err := meta.Put([]byte("journal-id"), []byte(s.journalID)); err != nil {
				return err
			}
		}
		if meta.Get([]byte("exposed_usage")) == nil {
			if err := putNumber(meta, "exposed_usage", tx.Bucket(usageBucket).Sequence()); err != nil {
				return err
			}
		}
		for _, field := range []string{"desired", "applied", "authority", "durable_usage", "accounted_usage", "spooled_bytes", "exposed_usage", "authority_sequence", "authority_acked", "authority_reported_at"} {
			if value := meta.Get([]byte(field)); value != nil && len(value) != 8 {
				return fmt.Errorf("corrupt node state field %s", field)
			}
		}
		if number(meta, "exposed_usage") > tx.Bucket(usageBucket).Sequence() {
			return ErrUsage
		}
		return meta.Put([]byte("identity"), encoded)
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Identity() Identity { return s.identity }
func (s *Store) JournalID() string  { return s.journalID }
func key(n uint64) []byte           { return binary.BigEndian.AppendUint64(nil, n) }
func number(b *bolt.Bucket, k string) uint64 {
	v := b.Get([]byte(k))
	if len(v) != 8 {
		return 0
	}
	return binary.BigEndian.Uint64(v)
}
func putNumber(b *bolt.Bucket, k string, n uint64) error { return b.Put([]byte(k), key(n)) }

func (s *Store) Cursors() (Cursors, error) {
	var result Cursors
	err := s.db.View(func(tx *bolt.Tx) error {
		m := tx.Bucket(metaBucket)
		result = Cursors{
			Desired: number(m, "desired"), Applied: number(m, "applied"), Authority: number(m, "authority"),
			DurableUsage: number(m, "durable_usage"), AccountedUsage: number(m, "accounted_usage"),
		}
		return nil
	})
	return result, err
}

func (s *Store) Fence(authority *nodev1.SessionAuthority) error {
	if authority == nil || authority.NodeId != s.identity.NodeID || authority.ControlPlaneId != s.identity.ControlPlaneID || authority.Generation == 0 {
		return ErrIdentity
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		meta := tx.Bucket(metaBucket)
		if authority.Generation < number(meta, "authority") {
			return ErrRevision
		}
		return putNumber(meta, "authority", authority.Generation)
	})
}

func (s *Store) Receive(d *nodev1.Deployment) error {
	if d == nil || d.NodeId != s.identity.NodeID || d.ControlPlaneId != s.identity.ControlPlaneID {
		return ErrIdentity
	}
	if d.Revision == 0 || d.AuthorityGeneration == 0 || len(d.Payload) == 0 || len(d.Payload) > MaxDeploymentBytes {
		return ErrRevision
	}
	digest := sha256.Sum256(d.Payload)
	if !bytes.Equal(digest[:], d.PayloadSha256) {
		return ErrDigest
	}
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(d)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		m := tx.Bucket(metaBucket)
		if d.AuthorityGeneration < number(m, "authority") || d.Revision < number(m, "desired") {
			return ErrRevision
		}
		deploys := tx.Bucket(deployBucket)
		if prev := deploys.Get(key(d.Revision)); prev != nil {
			var old nodev1.Deployment
			if err := proto.Unmarshal(prev, &old); err != nil {
				return err
			}
			if !bytes.Equal(old.PayloadSha256, d.PayloadSha256) || old.AuthorityGeneration > d.AuthorityGeneration {
				return ErrRevision
			}
		}
		if err := deploys.Put(key(d.Revision), encoded); err != nil {
			return err
		}
		var obsolete [][]byte
		applied := number(m, "applied")
		cursor := deploys.Cursor()
		for k, _ := cursor.First(); k != nil; k, _ = cursor.Next() {
			if !bytes.Equal(k, key(d.Revision)) && !bytes.Equal(k, key(applied)) {
				obsolete = append(obsolete, bytes.Clone(k))
			}
		}
		for _, k := range obsolete {
			if err := deploys.Delete(k); err != nil {
				return err
			}
		}
		if err := putNumber(m, "desired", d.Revision); err != nil {
			return err
		}
		return putNumber(m, "authority", d.AuthorityGeneration)
	})
}

func (s *Store) Desired() (*nodev1.Deployment, error) {
	var result *nodev1.Deployment
	err := s.db.View(func(tx *bolt.Tx) error {
		revision := number(tx.Bucket(metaBucket), "desired")
		if revision == 0 {
			return nil
		}
		result = new(nodev1.Deployment)
		data := tx.Bucket(deployBucket).Get(key(revision))
		if len(data) == 0 {
			return ErrRevision
		}
		return proto.Unmarshal(data, result)
	})
	return result, err
}

func (s *Store) Applied(generation, revision uint64, digest []byte) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		m := tx.Bucket(metaBucket)
		if generation != number(m, "authority") || revision != number(m, "desired") || revision < number(m, "applied") {
			return ErrRevision
		}
		d := new(nodev1.Deployment)
		data := tx.Bucket(deployBucket).Get(key(revision))
		if len(data) == 0 {
			return ErrRevision
		}
		if err := proto.Unmarshal(data, d); err != nil {
			return err
		}
		if d.AuthorityGeneration != generation || !bytes.Equal(d.PayloadSha256, digest) {
			return ErrRevision
		}
		if err := putNumber(m, "applied", revision); err != nil {
			return err
		}
		if err := m.Put([]byte("applied-digest"), digest); err != nil {
			return err
		}
		var obsolete [][]byte
		c := tx.Bucket(deployBucket).Cursor()
		for k, _ := c.First(); k != nil && bytes.Compare(k, key(revision)) < 0; k, _ = c.Next() {
			obsolete = append(obsolete, bytes.Clone(k))
		}
		for _, k := range obsolete {
			if err := tx.Bucket(deployBucket).Delete(k); err != nil {
				return err
			}
		}
		return nil
	})
}

func meterKey(c *nodev1.UsageCheckpoint) ([]byte, error) {
	if c == nil || !validID(c.GrantId) || !validID(c.ClientId) || !validID(c.TermId) || !validID(c.MeterIncarnation) || c.MeterId == "" ||
		len(c.MeterId) > 128 || c.ObservedUnixMilli <= 0 || proto.Size(c) > 4096 {
		return nil, ErrUsage
	}
	return json.Marshal([]string{c.GrantId, c.ClientId, c.TermId, c.MeterId, c.MeterIncarnation})
}

func (s *Store) AppendUsage(checkpoints []*nodev1.UsageCheckpoint) (uint64, error) {
	batch := &nodev1.UsageBatch{NodeId: s.identity.NodeID, InstallationId: s.identity.InstallationID, Checkpoints: checkpoints}
	var sequence uint64
	err := s.db.Update(func(tx *bolt.Tx) error {
		return appendUsage(tx, batch, &sequence)
	})
	if err != nil {
		return 0, err
	}
	return sequence, nil
}

func appendUsage(tx *bolt.Tx, batch *nodev1.UsageBatch, sequence *uint64) error {
	if len(batch.Checkpoints) == 0 || len(batch.Checkpoints) > 4096 || proto.Size(batch) > MaxBatchBytes-16 {
		return ErrUsage
	}
	seen := map[string]bool{}
	meters := tx.Bucket(metersBucket)
	for _, c := range batch.Checkpoints {
		k, err := meterKey(c)
		if err != nil {
			return err
		}
		if seen[string(k)] {
			return ErrUsage
		}
		seen[string(k)] = true
		if previous := meters.Get(k); previous != nil {
			var old nodev1.UsageCheckpoint
			if err := proto.Unmarshal(previous, &old); err != nil {
				return err
			}
			if c.Uploaded < old.Uploaded || c.Downloaded < old.Downloaded {
				return ErrUsage
			}
		}
		data, err := proto.Marshal(c)
		if err != nil {
			return err
		}
		if err := meters.Put(k, data); err != nil {
			return err
		}
	}
	return queueUsage(tx, batch, sequence)
}

// PendingUsage durably freezes batches before handing them to a sender.
func (s *Store) PendingUsage(limit int) ([]*nodev1.UsageBatch, error) {
	if limit < 1 || limit > 128 {
		return nil, ErrUsage
	}
	var result []*nodev1.UsageBatch
	read := func(tx *bolt.Tx) error {
		result = nil
		c := tx.Bucket(usageBucket).Cursor()
		for k, v := c.First(); k != nil && len(result) < limit; k, v = c.Next() {
			b := new(nodev1.UsageBatch)
			if err := proto.Unmarshal(v, b); err != nil {
				return err
			}
			if len(k) != 8 || b.Sequence != binary.BigEndian.Uint64(k) || b.NodeId != s.identity.NodeID || b.InstallationId != s.identity.InstallationID {
				return ErrUsage
			}
			result = append(result, b)
		}
		return nil
	}
	var frozen bool
	err := s.db.View(func(tx *bolt.Tx) error {
		if err := read(tx); err != nil {
			return err
		}
		frozen = len(result) == 0 || result[len(result)-1].Sequence <= number(tx.Bucket(metaBucket), "exposed_usage")
		return nil
	})
	if err != nil || frozen {
		return result, err
	}
	err = s.db.Update(func(tx *bolt.Tx) error {
		if err := read(tx); err != nil {
			return err
		}
		if len(result) == 0 {
			return nil
		}
		meta := tx.Bucket(metaBucket)
		return putNumber(meta, "exposed_usage", max(number(meta, "exposed_usage"), result[len(result)-1].Sequence))
	})
	return result, err
}

func (s *Store) Acknowledge(r *nodev1.UsageReceipt) error {
	if r == nil || r.AccountedSequence > r.DurableSequence {
		return ErrUsage
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		q := tx.Bucket(usageBucket)
		if r.DurableSequence > q.Sequence() {
			return ErrUsage
		}
		m := tx.Bucket(metaBucket)
		if r.DurableSequence > number(m, "exposed_usage") {
			return ErrUsage
		}
		durable := max(r.DurableSequence, number(m, "durable_usage"))
		accounted := max(r.AccountedSequence, number(m, "accounted_usage"))
		var remove [][]byte
		var reclaimed uint64
		c := q.Cursor()
		for k, v := c.First(); k != nil && bytes.Compare(k, key(durable)) <= 0; k, v = c.Next() {
			remove = append(remove, bytes.Clone(k))
			reclaimed += uint64(len(v))
		}
		for _, k := range remove {
			if err := q.Delete(k); err != nil {
				return err
			}
		}
		spooled := number(m, "spooled_bytes")
		if reclaimed > spooled {
			return ErrUsage
		}
		if err := putNumber(m, "spooled_bytes", spooled-reclaimed); err != nil {
			return err
		}
		if err := putNumber(m, "durable_usage", durable); err != nil {
			return err
		}
		return putNumber(m, "accounted_usage", accounted)
	})
}
