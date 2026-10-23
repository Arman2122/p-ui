package state

import (
	"bytes"
	"math"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/authority"
)

var authoritySeenBucket = []byte("authority_seen")

func (s *Store) AuthorityCursor() (string, error) {
	var cursor string
	err := s.db.View(func(tx *bolt.Tx) error {
		cursor = string(tx.Bucket(metaBucket).Get([]byte("authority_cursor")))
		return nil
	})
	if err == nil && cursor != "" && !validID(cursor) {
		return "", authority.ErrReport
	}
	return cursor, err
}

func decodeAuthority(data []byte) (*nodev1.AuthorityReport, error) {
	if data == nil {
		return nil, nil
	}
	report := new(nodev1.AuthorityReport)
	if len(data) > MaxBatchBytes || proto.Unmarshal(data, report) != nil || authority.ValidateReport(report, false) != nil {
		return nil, authority.ErrReport
	}
	return report, nil
}

func (s *Store) PendingAuthority() (*nodev1.AuthorityReport, error) {
	var report *nodev1.AuthorityReport
	err := s.db.View(func(tx *bolt.Tx) error {
		var err error
		report, err = decodeAuthority(tx.Bucket(metaBucket).Get([]byte("authority_pending")))
		return err
	})
	if err == nil && report != nil && (report.NodeId != s.identity.NodeID || report.InstallationId != s.identity.InstallationID || report.StateJournalId != s.journalID) {
		return nil, ErrIdentity
	}
	return report, err
}

func (s *Store) CaptureAuthority(journal *nodev1.RuntimeJournalIdentity, page authority.Page, ready bool, now int64) (*nodev1.AuthorityReport, error) {
	if now <= 0 || !authority.ValidRuntime(journal) || journal.StateJournalId != s.journalID || len(page.Grants) > authority.MaxEntries || page.Next != "" && !validID(page.Next) {
		return nil, authority.ErrReport
	}
	grants := make([]*nodev1.GrantAuthority, len(page.Grants))
	encoded := make([][]byte, len(grants))
	for i, grant := range page.Grants {
		grants[i] = authority.ProtoGrant(grant)
		if authority.ValidateGrant(grants[i]) != nil || i > 0 && grants[i-1].GrantId >= grant.GrantID {
			return nil, authority.ErrReport
		}
		var err error
		encoded[i], err = proto.MarshalOptions{Deterministic: true}.Marshal(grants[i])
		if err != nil {
			return nil, err
		}
	}
	var result *nodev1.AuthorityReport
	err := s.db.Update(func(tx *bolt.Tx) error {
		meta := tx.Bucket(metaBucket)
		pending, err := decodeAuthority(meta.Get([]byte("authority_pending")))
		if err != nil {
			return err
		}
		if pending != nil {
			result = pending
			return nil
		}
		seen, err := tx.CreateBucketIfNotExists(authoritySeenBucket)
		if err != nil {
			return err
		}
		changed := make([]*nodev1.GrantAuthority, 0, len(grants))
		for i, grant := range grants {
			if !bytes.Equal(seen.Get([]byte(grant.GrantId)), encoded[i]) {
				changed = append(changed, grant)
			}
		}
		if err := meta.Put([]byte("authority_cursor"), []byte(page.Next)); err != nil {
			return err
		}
		last := int64(number(meta, "authority_reported_at"))
		wasReady := bytes.Equal(meta.Get([]byte("authority_ready")), []byte{1})
		if len(changed) == 0 && ready == wasReady && last > 0 && now >= last && now-last < 10000 {
			return nil
		}
		sequence := number(meta, "authority_sequence")
		if sequence >= math.MaxInt64 {
			return authority.ErrReport
		}
		result = &nodev1.AuthorityReport{NodeId: s.identity.NodeID, InstallationId: s.identity.InstallationID, RuntimeId: journal.RuntimeId, StateJournalId: s.journalID, CreditJournalId: journal.CreditJournalId, Sequence: sequence + 1, ObservedUnixMilli: now, RuntimeReady: ready, Grants: changed}
		if authority.ValidateReport(result, false) != nil {
			return authority.ErrReport
		}
		data, err := proto.MarshalOptions{Deterministic: true}.Marshal(result)
		if err != nil {
			return err
		}
		if len(data) > MaxBatchBytes {
			return ErrSpoolFull
		}
		if err := putNumber(meta, "authority_sequence", sequence+1); err != nil {
			return err
		}
		return meta.Put([]byte("authority_pending"), data)
	})
	return result, err
}

func (s *Store) AcknowledgeAuthority(receipt *nodev1.AuthorityReceipt) error {
	if receipt == nil || receipt.Sequence == 0 || receipt.Sequence > math.MaxInt64 || receipt.StateJournalId != s.journalID {
		return authority.ErrReport
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		meta := tx.Bucket(metaBucket)
		if !bytes.Equal(meta.Get([]byte("runtime-journal:"+receipt.RuntimeId)), []byte(receipt.CreditJournalId)) {
			return ErrIdentity
		}
		if receipt.Sequence <= number(meta, "authority_acked") {
			return nil
		}
		pending, err := decodeAuthority(meta.Get([]byte("authority_pending")))
		if err != nil {
			return err
		}
		if pending == nil || pending.Sequence != receipt.Sequence || pending.RuntimeId != receipt.RuntimeId || pending.CreditJournalId != receipt.CreditJournalId {
			return authority.ErrReport
		}
		seen, err := tx.CreateBucketIfNotExists(authoritySeenBucket)
		if err != nil {
			return err
		}
		for _, grant := range pending.Grants {
			data, err := proto.MarshalOptions{Deterministic: true}.Marshal(grant)
			if err != nil {
				return err
			}
			if err := seen.Put([]byte(grant.GrantId), data); err != nil {
				return err
			}
		}
		if err := putNumber(meta, "authority_acked", receipt.Sequence); err != nil {
			return err
		}
		if err := putNumber(meta, "authority_reported_at", uint64(pending.ObservedUnixMilli)); err != nil {
			return err
		}
		ready := []byte{0}
		if pending.RuntimeReady {
			ready[0] = 1
		}
		if err := meta.Put([]byte("authority_ready"), ready); err != nil {
			return err
		}
		return meta.Delete([]byte("authority_pending"))
	})
}
