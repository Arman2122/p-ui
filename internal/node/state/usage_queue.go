package state

import (
	"encoding/binary"
	"fmt"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
)

type usageEdit struct {
	batch         *nodev1.UsageBatch
	indices       map[string]int
	size          int
	previousBytes uint64
}

func checkpointSize(checkpoint *nodev1.UsageCheckpoint) int {
	n := proto.Size(checkpoint)
	return protowire.SizeTag(4) + protowire.SizeVarint(uint64(n)) + n
}

func queueUsage(tx *bolt.Tx, incoming *nodev1.UsageBatch, sequence *uint64) error {
	queue, index, meta := tx.Bucket(usageBucket), tx.Bucket(mutableBucket), tx.Bucket(metaBucket)
	exposed := number(meta, "exposed_usage")
	edits := make(map[uint64]*usageEdit)
	load := func(sequence uint64) (*usageEdit, error) {
		if edit := edits[sequence]; edit != nil {
			return edit, nil
		}
		data := queue.Get(key(sequence))
		if data == nil {
			return nil, ErrUsage
		}
		batch := new(nodev1.UsageBatch)
		if err := proto.Unmarshal(data, batch); err != nil {
			return nil, err
		}
		if batch.Sequence != sequence || batch.NodeId != incoming.NodeId || batch.InstallationId != incoming.InstallationId {
			return nil, ErrIdentity
		}
		edit := &usageEdit{batch: batch, indices: make(map[string]int), size: proto.Size(batch), previousBytes: uint64(len(data))}
		for i, checkpoint := range batch.Checkpoints {
			key, err := meterKey(checkpoint)
			if err != nil {
				return nil, err
			}
			if _, ok := edit.indices[string(key)]; ok {
				return nil, ErrUsage
			}
			edit.indices[string(key)] = i
		}
		edits[sequence] = edit
		return edit, nil
	}
	newBatch := func() (*usageEdit, error) {
		sequence, err := queue.NextSequence()
		if err != nil {
			return nil, err
		}
		if sequence == 0 {
			return nil, fmt.Errorf("%w: sequence exhausted", ErrUsage)
		}
		batch := &nodev1.UsageBatch{NodeId: incoming.NodeId, InstallationId: incoming.InstallationId, Sequence: sequence}
		edit := &usageEdit{batch: batch, indices: make(map[string]int), size: proto.Size(batch)}
		edits[sequence] = edit
		return edit, nil
	}
	for _, checkpoint := range incoming.Checkpoints {
		meter, err := meterKey(checkpoint)
		if err != nil {
			return err
		}
		var previous uint64
		if encoded := index.Get(meter); encoded != nil {
			if len(encoded) != 8 {
				return ErrUsage
			}
			previous = binary.BigEndian.Uint64(encoded)
		}
		if previous > exposed {
			edit, err := load(previous)
			if err != nil {
				return err
			}
			position, ok := edit.indices[string(meter)]
			if !ok {
				return ErrUsage
			}
			edit.size += checkpointSize(checkpoint) - checkpointSize(edit.batch.Checkpoints[position])
			if edit.size > MaxBatchBytes {
				return ErrUsage
			}
			edit.batch.Checkpoints[position] = checkpoint
			continue
		}
		var tail *usageEdit
		if queue.Sequence() > exposed {
			tail, err = load(queue.Sequence())
			if err != nil {
				return err
			}
		}
		if tail == nil || len(tail.batch.Checkpoints) >= 4096 || tail.size+checkpointSize(checkpoint) > MaxBatchBytes/2 {
			tail, err = newBatch()
			if err != nil {
				return err
			}
		}
		tail.indices[string(meter)] = len(tail.batch.Checkpoints)
		tail.batch.Checkpoints = append(tail.batch.Checkpoints, checkpoint)
		tail.size += checkpointSize(checkpoint)
		if err := index.Put(meter, key(tail.batch.Sequence)); err != nil {
			return err
		}
	}
	spooled := number(meta, "spooled_bytes")
	if spooled > MaxSpoolBytes {
		return ErrSpoolFull
	}
	encoded := make(map[uint64][]byte, len(edits))
	var replaced, added uint64
	for _, edit := range edits {
		data, err := proto.MarshalOptions{Deterministic: true}.Marshal(edit.batch)
		if err != nil {
			return err
		}
		if len(data) > MaxBatchBytes {
			return ErrUsage
		}
		replaced += edit.previousBytes
		added += uint64(len(data))
		encoded[edit.batch.Sequence] = data
	}
	if replaced > spooled {
		return ErrUsage
	}
	spooled -= replaced
	if added > MaxSpoolBytes-spooled {
		return ErrSpoolFull
	}
	spooled += added
	for sequence, data := range encoded {
		if err := queue.Put(key(sequence), data); err != nil {
			return err
		}
	}
	*sequence = queue.Sequence()
	return putNumber(meta, "spooled_bytes", spooled)
}
