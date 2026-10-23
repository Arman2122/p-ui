package governor

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	bolt "go.etcd.io/bbolt"

	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/node/privatefile"
)

var (
	ErrGrant     = errors.New("invalid runtime grant")
	grantsBucket = []byte("grants")
)

const (
	minimumReservationBlock = 256 << 10
	maximumReservationBlock = 8 << 20
)

type Grant struct {
	ID           string
	ClientID     string
	RateRevision uint64
	Policy       limits.Policy
}

type record struct {
	Grant     Grant
	Reserved  uint64
	Suspended bool
}

type Store struct {
	mu        sync.Mutex
	db        *bolt.DB
	accounts  map[string]*limits.Account
	clients   map[string]string
	journalID string
	clock     limits.Clock
}

func (s *Store) SetClock(clock limits.Clock) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clock = clock
	for _, account := range s.accounts {
		account.SetClock(clock)
	}
}

type backing struct {
	store    *Store
	id       string
	reserved uint64
}

func validID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed != uuid.Nil && parsed.String() == id
}

func Validate(grant Grant) error {
	if !validID(grant.ID) || !validID(grant.ClientID) || !validID(grant.Policy.Term) || grant.Policy.AuthorityRevision != 0 && grant.RateRevision != grant.Policy.AuthorityRevision {
		return ErrGrant
	}
	_, err := limits.New(grant.Policy)
	return err
}

func Open(path string) (*Store, error) {
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, ErrGrant
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
	db.MaxBatchSize = 1024
	db.MaxBatchDelay = 2 * time.Millisecond
	s := &Store{db: db, accounts: make(map[string]*limits.Account), clients: make(map[string]string)}
	err = db.Update(func(tx *bolt.Tx) error {
		meta, err := tx.CreateBucketIfNotExists([]byte("identity"))
		if err != nil {
			return err
		}
		if data := meta.Get([]byte("journal-id")); data != nil {
			s.journalID = string(data)
			if !validID(s.journalID) {
				return ErrGrant
			}
		} else {
			s.journalID = uuid.NewString()
			if err := meta.Put([]byte("journal-id"), []byte(s.journalID)); err != nil {
				return err
			}
		}
		bucket, err := tx.CreateBucketIfNotExists(grantsBucket)
		if err != nil {
			return err
		}
		return bucket.ForEach(func(key, data []byte) error {
			var r record
			if err := json.Unmarshal(data, &r); err != nil || r.Grant.ID != string(key) {
				return ErrGrant
			}
			return s.restore(r)
		})
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) restore(r record) error {
	if Validate(r.Grant) != nil {
		return ErrGrant
	}
	if r.Grant.Policy.Enabled && !r.Suspended {
		if _, ok := s.clients[r.Grant.ClientID]; ok {
			return ErrGrant
		}
		s.clients[r.Grant.ClientID] = r.Grant.ID
	}
	a, err := limits.Restore(r.Grant.Policy, r.Reserved, &backing{store: s, id: r.Grant.ID, reserved: r.Reserved})
	if err != nil {
		return err
	}
	if r.Suspended {
		a.Quarantine()
	}
	a.SetClock(s.clock)
	s.accounts[r.Grant.ID] = a
	return nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) JournalID() string { return s.journalID }

func (s *Store) Apply(grant Grant) error {
	if Validate(grant) != nil {
		return ErrGrant
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing := s.clients[grant.ClientID]; grant.Policy.Enabled && existing != "" && existing != grant.ID {
		return ErrGrant
	}
	var saved record
	if err := s.db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(grantsBucket).Get([]byte(grant.ID))
		if data == nil {
			return nil
		}
		return json.Unmarshal(data, &saved)
	}); err != nil {
		return err
	}
	if saved.Grant.ID != "" && (saved.Grant.ClientID != grant.ClientID || saved.Grant.Policy.Term != grant.Policy.Term) {
		return ErrGrant
	}
	if saved.Suspended {
		return ErrGrant
	}
	if a := s.accounts[grant.ID]; a != nil {
		grant.Policy = limits.RetainCredit(grant.Policy, a.Snapshot().Policy)
		if err := a.Apply(grant.Policy); err != nil {
			return err
		}
	} else {
		r := record{Grant: grant}
		if err := s.db.Update(func(tx *bolt.Tx) error { return save(tx, r) }); err != nil {
			return err
		}
		if err := s.restore(r); err != nil {
			return err
		}
	}
	if grant.Policy.Enabled {
		s.clients[grant.ClientID] = grant.ID
	} else if s.clients[grant.ClientID] == grant.ID {
		delete(s.clients, grant.ClientID)
	}
	return nil
}

func save(tx *bolt.Tx, r record) error {
	encoded, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return tx.Bucket(grantsBucket).Put([]byte(r.Grant.ID), encoded)
}

func (s *Store) Lookup(client string) (*limits.Account, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a := s.accounts[s.clients[client]]
	return a, a != nil
}

func (b *backing) StorePolicy(policy limits.Policy) error {
	return b.store.db.Update(func(tx *bolt.Tx) error {
		var r record
		if err := json.Unmarshal(tx.Bucket(grantsBucket).Get([]byte(b.id)), &r); err != nil {
			return err
		}
		if r.Grant.Policy.Term != policy.Term || policy.AuthorityRevision < r.Grant.Policy.AuthorityRevision || r.Grant.Policy.Revision >= policy.Revision && !limits.CreditAdvance(r.Grant.Policy, policy) && !limits.AuthorityAdvance(r.Grant.Policy, policy) {
			return limits.ErrRevision
		}
		r.Grant.Policy = policy
		if policy.AuthorityRevision != 0 {
			r.Grant.RateRevision = policy.AuthorityRevision
		}
		return save(tx, r)
	})
}

func (b *backing) Reserve(policy limits.Policy, total uint64) error {
	if total <= b.reserved {
		return nil
	}
	next := total
	block := reservationSize(policy)
	if next <= math.MaxUint64-block {
		next += block
	}
	if !policy.QuotaUnlimited {
		next = min(next, policy.CreditCeiling)
	}
	err := b.store.db.Batch(func(tx *bolt.Tx) error {
		var r record
		if err := json.Unmarshal(tx.Bucket(grantsBucket).Get([]byte(b.id)), &r); err != nil {
			return err
		}
		if r.Suspended || r.Grant.Policy != policy || r.Reserved > next || !policy.QuotaUnlimited && total > policy.CreditCeiling {
			return ErrGrant
		}
		r.Reserved = next
		return save(tx, r)
	})
	if err == nil {
		b.reserved = next
	}
	return err
}

func reservationSize(policy limits.Policy) uint64 {
	if policy.Upload.Mode == limits.Unlimited || policy.Download.Mode == limits.Unlimited {
		return maximumReservationBlock
	}
	return max(minimumReservationBlock, min(maximumReservationBlock, (policy.Upload.BitsPerSecond+policy.Download.BitsPerSecond)/80))
}

func (s *Store) Reconcile(grants []Grant) error {
	wanted := make(map[string]bool, len(grants))
	clients := make(map[string]bool, len(grants))
	for _, grant := range grants {
		if Validate(grant) != nil || wanted[grant.ID] || clients[grant.ClientID] {
			return ErrGrant
		}
		wanted[grant.ID], clients[grant.ClientID] = true, true
	}
	s.mu.Lock()
	err := s.db.Update(func(tx *bolt.Tx) error {
		for id := range s.accounts {
			if wanted[id] {
				continue
			}
			var r record
			if err := json.Unmarshal(tx.Bucket(grantsBucket).Get([]byte(id)), &r); err != nil {
				return err
			}
			r.Suspended = true
			if err := save(tx, r); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		for id, account := range s.accounts {
			if !wanted[id] {
				account.Quarantine()
			}
		}
		for client, id := range s.clients {
			if !wanted[id] {
				delete(s.clients, client)
			}
		}
	}
	s.mu.Unlock()
	if err != nil {
		return err
	}
	for _, grant := range grants {
		if err := s.Apply(grant); err != nil {
			return err
		}
	}
	return nil
}

type Observation struct {
	Grant    Grant
	Snapshot limits.Snapshot
}

func (s *Store) Observe() ([]Observation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]Observation, 0, len(s.accounts))
	err := s.db.View(func(tx *bolt.Tx) error {
		for id := range s.accounts {
			var r record
			if err := json.Unmarshal(tx.Bucket(grantsBucket).Get([]byte(id)), &r); err != nil {
				return err
			}
			result = append(result, Observation{Grant: r.Grant})
		}
		return nil
	})
	if err == nil {
		for i := range result {
			result[i].Snapshot = s.accounts[result[i].Grant.ID].Snapshot()
		}
	}
	return result, err
}
