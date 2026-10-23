package native

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/google/uuid"
	bolt "go.etcd.io/bbolt"

	"github.com/Arman2122/p-ui/internal/node/compatibility"
	"github.com/Arman2122/p-ui/internal/node/governor"
	"github.com/Arman2122/p-ui/internal/node/limits"
	"github.com/Arman2122/p-ui/internal/node/privatefile"
	"github.com/Arman2122/p-ui/internal/node/state"
)

var (
	grantsBucket  = []byte("native-grants")
	clientsBucket = []byte("native-clients")
)

type clientRecord struct {
	Class       uint16
	ActiveGrant string
}

type grantRecord struct {
	Grant            governor.Grant
	Class            uint16
	Reserved         uint64
	Lease            *Lease
	Retired          bool
	Suspended        bool
	SelectorsPending bool
	PendingAuthority *authorityChange
}

type Journal struct {
	db        *bolt.DB
	journalID string
}

func Open(path string) (*Journal, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: time.Second, OpenFile: privatefile.Open})
	if err != nil {
		return nil, err
	}
	j := &Journal{db: db}
	if err := db.Update(func(tx *bolt.Tx) error {
		meta := tx.Bucket([]byte("identity"))
		if meta == nil {
			if name, _ := tx.Cursor().First(); name != nil {
				return state.ErrStateFormat
			}
		} else if version := meta.Get([]byte("state-format")); len(version) != 8 || binary.BigEndian.Uint64(version) != uint64(compatibility.StateFormat) {
			return state.ErrStateFormat
		}
		meta, err := tx.CreateBucketIfNotExists([]byte("identity"))
		if err != nil {
			return err
		}
		if err := meta.Put([]byte("state-format"), binary.BigEndian.AppendUint64(nil, uint64(compatibility.StateFormat))); err != nil {
			return err
		}
		if data := meta.Get([]byte("journal-id")); data != nil {
			j.journalID = string(data)
			if !validID(j.journalID) {
				return ErrAuthority
			}
		} else {
			j.journalID = uuid.NewString()
			if err := meta.Put([]byte("journal-id"), []byte(j.journalID)); err != nil {
				return err
			}
		}
		if _, err := tx.CreateBucketIfNotExists(grantsBucket); err != nil {
			return err
		}
		_, err = tx.CreateBucketIfNotExists(clientsBucket)
		return err
	}); err != nil {
		_ = db.Close()
		return nil, err
	}
	return j, nil
}

func (j *Journal) Close() error { return j.db.Close() }

func (j *Journal) JournalID() string { return j.journalID }

func (j *Journal) Bind(identity state.Identity, meter string) error {
	if !validID(identity.NodeID) || !validID(identity.InstallationID) || !validID(identity.ControlPlaneID) || meter == "" || len(meter) > 48 {
		return ErrAuthority
	}
	data, err := json.Marshal(struct {
		Identity state.Identity
		Meter    string
	}{identity, meter})
	if err != nil {
		return err
	}
	return j.db.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists([]byte("identity"))
		if err != nil {
			return err
		}
		if existing := bucket.Get([]byte("owner")); existing != nil && !bytes.Equal(existing, data) {
			return state.ErrIdentity
		}
		return bucket.Put([]byte("owner"), data)
	})
}

func (j *Journal) Suspend(id string) error {
	return j.db.Update(func(tx *bolt.Tx) error {
		r, err := readGrant(tx, id)
		if err != nil {
			return err
		}
		if r.Lease != nil {
			return ErrBusy
		}
		if r.PendingAuthority != nil {
			return ErrBusy
		}
		r.Suspended = true
		clients := tx.Bucket(clientsBucket)
		var client clientRecord
		if err := json.Unmarshal(clients.Get([]byte(r.Grant.ClientID)), &client); err != nil {
			return err
		}
		if client.ActiveGrant == id {
			client.ActiveGrant = ""
			data, _ := json.Marshal(client)
			if err := clients.Put([]byte(r.Grant.ClientID), data); err != nil {
				return err
			}
		}
		return saveGrant(tx, r)
	})
}

func readGrant(tx *bolt.Tx, id string) (grantRecord, error) {
	var r grantRecord
	data := tx.Bucket(grantsBucket).Get([]byte(id))
	if data == nil {
		return r, ErrAuthority
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return r, err
	}
	if r.Grant.ID != id || governor.Validate(r.Grant) != nil || r.Class == 0 || r.Class > MaxClasses {
		return r, ErrAuthority
	}
	if err := validateAuthorityChange(r); err != nil {
		return r, err
	}
	return r, nil
}

func saveGrant(tx *bolt.Tx, r grantRecord) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return tx.Bucket(grantsBucket).Put([]byte(r.Grant.ID), data)
}

func (j *Journal) Apply(grant governor.Grant) error {
	if err := governor.Validate(grant); err != nil {
		return err
	}
	return j.db.Update(func(tx *bolt.Tx) error {
		return applyGrant(tx, grant)
	})
}

func applyGrant(tx *bolt.Tx, grant governor.Grant) error {
	bucket := tx.Bucket(grantsBucket)
	if bucket.Get([]byte(grant.ID)) == nil {
		clients := tx.Bucket(clientsBucket)
		var client clientRecord
		if data := clients.Get([]byte(grant.ClientID)); data != nil {
			if err := json.Unmarshal(data, &client); err != nil {
				return err
			}
			if client.ActiveGrant != "" || client.Class == 0 || client.Class > MaxClasses {
				return ErrAuthority
			}
		} else {
			sequence, err := clients.NextSequence()
			if err != nil {
				return err
			}
			if sequence > MaxClasses {
				return ErrAuthority
			}
			client.Class = uint16(sequence)
		}
		client.ActiveGrant = grant.ID
		data, _ := json.Marshal(client)
		if err := clients.Put([]byte(grant.ClientID), data); err != nil {
			return err
		}
		return saveGrant(tx, grantRecord{Grant: grant, Class: client.Class})
	}
	r, err := readGrant(tx, grant.ID)
	if err != nil {
		return err
	}
	if r.Suspended || r.Grant.ClientID != grant.ClientID || r.Grant.Policy.Term != grant.Policy.Term {
		return ErrAuthority
	}
	if r.PendingAuthority != nil {
		return ErrBusy
	}
	if r.Grant == grant {
		return nil
	}
	if grant.Policy.Revision <= r.Grant.Policy.Revision || grant.Policy.CreditRevision < r.Grant.Policy.CreditRevision || grant.RateRevision < r.Grant.RateRevision {
		return limits.ErrRevision
	}
	if r.Lease != nil {
		return ErrBusy
	}
	r.Grant = grant
	return saveGrant(tx, r)
}

func (j *Journal) Reserve(id string, selectors []Selector, bytes uint64, now time.Time) (Lease, error) {
	return j.reserve(id, selectors, bytes, now, false)
}

func (j *Journal) reserve(id string, selectors []Selector, bytes uint64, now time.Time, authorityMutation bool) (Lease, error) {
	var lease Lease
	if validateSelectors(selectors) != nil || bytes == 0 || bytes > MaxSliceBytes {
		return lease, ErrAuthority
	}
	err := j.db.Update(func(tx *bolt.Tx) error {
		network, err := readNetwork(tx)
		if err != nil {
			return err
		}
		if network.Pending != nil {
			return ErrBusy
		}
		r, err := readGrant(tx, id)
		if err != nil {
			return err
		}
		if r.Suspended || !r.Grant.Policy.Enabled {
			return limits.ErrBlocked
		}
		if r.PendingAuthority != nil && !authorityMutation || authorityMutation && r.PendingAuthority == nil {
			return ErrBusy
		}
		if r.Lease != nil {
			return ErrBusy
		}
		grant := r.Grant
		if authorityMutation {
			grant = r.PendingAuthority.Grant
		}
		p := grant.Policy
		expires := now.Add(MaxLeaseTime).UnixMilli()
		if p.ExpiresUnixMilli != 0 {
			expires = min(expires, p.ExpiresUnixMilli)
		}
		if expires <= now.UnixMilli() {
			return limits.ErrExpired
		}
		if !p.QuotaUnlimited {
			if r.Reserved >= p.CreditCeiling {
				return limits.ErrQuota
			}
			bytes = min(bytes, p.CreditCeiling-r.Reserved)
		}
		if bytes > math.MaxUint64-r.Reserved {
			return ErrAuthority
		}
		lease = Lease{ID: uuid.NewString(), Grant: grant, Class: r.Class, Bytes: bytes, ExpiresUnixMilli: expires, Selectors: slices.Clone(selectors)}
		if err := lease.validate(now); err != nil {
			return err
		}
		r.Reserved += bytes
		r.Lease, r.Retired = &lease, false
		return saveGrant(tx, r)
	})
	if err != nil {
		return Lease{}, err
	}
	return lease, nil
}

// Finalize follows kernel blocking and a durable final usage checkpoint.
// Reservations remain consumed even if the kernel lost its counters.
func (j *Journal) Finalize(id, leaseID string) error {
	return j.db.Update(func(tx *bolt.Tx) error {
		r, err := readGrant(tx, id)
		if err != nil {
			return err
		}
		if r.Lease == nil || r.Lease.ID != leaseID {
			return ErrAuthority
		}
		r.Retired = true
		return saveGrant(tx, r)
	})
}

func (j *Journal) Forget(id, leaseID string) error {
	return j.db.Update(func(tx *bolt.Tx) error {
		r, err := readGrant(tx, id)
		if err != nil {
			return err
		}
		if r.Lease == nil || r.Lease.ID != leaseID || !r.Retired {
			return ErrAuthority
		}
		r.Lease, r.Retired, r.SelectorsPending = nil, false, false
		return saveGrant(tx, r)
	})
}

func (j *Journal) Records() ([]grantRecord, error) {
	var result []grantRecord
	err := j.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(grantsBucket).ForEach(func(key, _ []byte) error {
			r, err := readGrant(tx, string(key))
			if err == nil {
				result = append(result, r)
			}
			return err
		})
	})
	return result, err
}

func (j *Journal) selectors(id, leaseID string, selectors []Selector, pending bool) error {
	return j.db.Update(func(tx *bolt.Tx) error {
		r, err := readGrant(tx, id)
		if err != nil {
			return err
		}
		if r.Lease == nil || r.Lease.ID != leaseID || r.Retired || r.Suspended || r.PendingAuthority != nil || len(selectors) > 512 {
			return ErrAuthority
		}
		r.Lease.Selectors = slices.Clone(selectors)
		r.SelectorsPending = pending
		return saveGrant(tx, r)
	})
}
