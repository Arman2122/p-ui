package state

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"math"
	"slices"
	"strings"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/core"
)

type MeterDelta struct {
	Identity   *nodev1.UsageCheckpoint
	Uploaded   uint64
	Downloaded uint64
}

func (s *Store) LoadCounter(source string) (*core.Counter, error) {
	if !validCounterSource(source) {
		return nil, core.ErrCounterCheckpoint
	}
	var checkpoint core.CounterCheckpoint
	err := s.db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(countersBucket).Get([]byte(source))
		if data == nil {
			return nil
		}
		return json.Unmarshal(data, &checkpoint)
	})
	if err != nil {
		return nil, err
	}
	return core.CounterFromCheckpoint(checkpoint)
}

func validCounterSource(source string) bool {
	return source != "" && len(source) <= 128 && !strings.ContainsRune(source, 0)
}

func encodeCheckpoint(checkpoint core.CounterCheckpoint) ([]byte, error) {
	counter, err := core.CounterFromCheckpoint(checkpoint)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(counter.Checkpoint())
	if err != nil {
		return nil, err
	}
	if len(encoded) > MaxDeploymentBytes {
		return nil, core.ErrCounterCheckpoint
	}
	return encoded, nil
}

func deltaDigest(deltas []MeterDelta) ([32]byte, error) {
	checkpoints := make([]*nodev1.UsageCheckpoint, len(deltas))
	for i, delta := range deltas {
		if _, err := meterKey(delta.Identity); err != nil {
			return [32]byte{}, err
		}
		if delta.Identity.Uploaded != 0 || delta.Identity.Downloaded != 0 {
			return [32]byte{}, ErrUsage
		}
		checkpoint := proto.Clone(delta.Identity).(*nodev1.UsageCheckpoint)
		checkpoint.ObservedUnixMilli = 1
		checkpoint.Uploaded, checkpoint.Downloaded = delta.Uploaded, delta.Downloaded
		checkpoints[i] = checkpoint
	}
	slices.SortFunc(checkpoints, func(a, b *nodev1.UsageCheckpoint) int {
		first, _ := meterKey(a)
		second, _ := meterKey(b)
		return bytes.Compare(first, second)
	})
	for i := 1; i < len(checkpoints); i++ {
		previous, _ := meterKey(checkpoints[i-1])
		current, _ := meterKey(checkpoints[i])
		if bytes.Equal(previous, current) {
			return [32]byte{}, ErrUsage
		}
	}
	data, err := proto.MarshalOptions{Deterministic: true}.Marshal(&nodev1.UsageBatch{Checkpoints: checkpoints})
	if err != nil {
		return [32]byte{}, err
	}
	if len(data) > MaxDeploymentBytes {
		return [32]byte{}, ErrUsage
	}
	return sha256.Sum256(data), nil
}

func (s *Store) CommitCounterUsage(source string, previous, checkpoint core.CounterCheckpoint, deltas []MeterDelta) error {
	if !validCounterSource(source) || len(deltas) > 131072 {
		return core.ErrCounterCheckpoint
	}
	before, err := encodeCheckpoint(previous)
	if err != nil {
		return err
	}
	encoded, err := encodeCheckpoint(checkpoint)
	if err != nil {
		return err
	}
	digest, err := deltaDigest(deltas)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(countersBucket)
		current := bucket.Get([]byte(source))
		if current == nil {
			current, _ = encodeCheckpoint(core.CounterCheckpoint{})
		}
		if bytes.Equal(current, encoded) {
			if len(deltas) == 0 || bytes.Equal(bucket.Get([]byte(source+"\x00digest")), digest[:]) {
				return nil
			}
			return core.ErrCounterCheckpoint
		}
		if !bytes.Equal(current, before) {
			return core.ErrCounterCheckpoint
		}
		var checkpoints []*nodev1.UsageCheckpoint
		for _, delta := range deltas {
			key, err := meterKey(delta.Identity)
			if err != nil {
				return err
			}
			if delta.Identity.Uploaded != 0 || delta.Identity.Downloaded != 0 {
				return ErrUsage
			}
			cumulative := proto.Clone(delta.Identity).(*nodev1.UsageCheckpoint)
			if data := tx.Bucket(metersBucket).Get(key); data != nil {
				previous := new(nodev1.UsageCheckpoint)
				if err := proto.Unmarshal(data, previous); err != nil {
					return err
				}
				cumulative.Uploaded, cumulative.Downloaded = previous.Uploaded, previous.Downloaded
			}
			if delta.Uploaded > math.MaxUint64-cumulative.Uploaded || delta.Downloaded > math.MaxUint64-cumulative.Downloaded {
				return ErrUsage
			}
			cumulative.Uploaded += delta.Uploaded
			cumulative.Downloaded += delta.Downloaded
			checkpoints = append(checkpoints, cumulative)
		}
		for len(checkpoints) != 0 {
			size := min(1024, len(checkpoints))
			batch := &nodev1.UsageBatch{NodeId: s.identity.NodeID, InstallationId: s.identity.InstallationID, Checkpoints: checkpoints[:size]}
			var sequence uint64
			if err := appendUsage(tx, batch, &sequence); err != nil {
				return err
			}
			checkpoints = checkpoints[size:]
		}
		if err := bucket.Put([]byte(source), encoded); err != nil {
			return err
		}
		return bucket.Put([]byte(source+"\x00digest"), digest[:])
	})
}
