package state

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/authority"
)

func TestAuthorityPendingSurvivesRestartAndUsesExactReceipts(t *testing.T) {
	id := identity()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path, id)
	if err != nil {
		t.Fatal(err)
	}
	journal := &nodev1.RuntimeJournalIdentity{RuntimeId: "xray", CoreId: "xray", StateJournalId: s.JournalID(), CreditJournalId: uuid.NewString()}
	if err := s.BindRuntimeJournal(journal.RuntimeId, journal.CreditJournalId); err != nil {
		t.Fatal(err)
	}
	grant := authority.Grant{GrantID: uuid.NewString(), ClientID: uuid.NewString(), TermID: uuid.NewString(), PolicyRevision: 1, CreditCeiling: 100, ReservedHighWater: 100, AdmissionHighWater: 1, AdmissionKnown: true, SpendableBytes: 99, SpendableKnown: true, AuthorizationActive: true}
	page := authority.Page{Grants: []authority.Grant{grant}}
	first, err := s.CaptureAuthority(journal, page, true, 100000)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path, id)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	page.Grants[0].AdmissionHighWater = 2
	page.Grants[0].SpendableBytes = 98
	replay, err := s.CaptureAuthority(journal, page, false, 100001)
	if err != nil || !proto.Equal(first, replay) || s.JournalID() != journal.StateJournalId {
		t.Fatalf("pending changed across restart: %v %v", replay, err)
	}
	receipt := &nodev1.AuthorityReceipt{RuntimeId: journal.RuntimeId, StateJournalId: journal.StateJournalId, CreditJournalId: journal.CreditJournalId, Sequence: first.Sequence + 1}
	if err := s.AcknowledgeAuthority(receipt); !errors.Is(err, authority.ErrReport) {
		t.Fatalf("future receipt=%v", err)
	}
	receipt.Sequence = first.Sequence
	if err := s.AcknowledgeAuthority(receipt); err != nil {
		t.Fatal(err)
	}
	second, err := s.CaptureAuthority(journal, page, true, 100000)
	if err != nil || second.Sequence != 2 || len(second.Grants) != 1 || second.Grants[0].AdmissionHighWater != 2 {
		t.Fatalf("delta=%v %v", second, err)
	}
	if err := s.AcknowledgeAuthority(receipt); err != nil {
		t.Fatal(err)
	}
	if pending, _ := s.PendingAuthority(); !proto.Equal(pending, second) {
		t.Fatal("duplicate old receipt erased newer pending report")
	}
	receipt.Sequence = 2
	if err := s.AcknowledgeAuthority(receipt); err != nil {
		t.Fatal(err)
	}
	if unchanged, err := s.CaptureAuthority(journal, page, true, 100000); err != nil || unchanged != nil {
		t.Fatalf("unchanged grants emitted: %v %v", unchanged, err)
	}
	if err := s.db.Update(func(tx *bolt.Tx) error { return putNumber(tx.Bucket(metaBucket), "authority_reported_at", 1) }); err != nil {
		t.Fatal(err)
	}
	heartbeat, err := s.CaptureAuthority(journal, page, true, 100000)
	if err != nil || heartbeat.Sequence != 3 || len(heartbeat.Grants) != 0 || !heartbeat.RuntimeReady {
		t.Fatalf("sparse heartbeat=%v %v", heartbeat, err)
	}
	if batches, err := s.PendingUsage(1); err != nil || len(batches) != 0 {
		t.Fatalf("authority became usage=%v %v", batches, err)
	}
}

func TestRuntimeJournalPairRejectsCompleteWorkerStateReplacement(t *testing.T) {
	id := identity()
	path := filepath.Join(t.TempDir(), "agent.db")
	s, err := Open(path, id)
	if err != nil {
		t.Fatal(err)
	}
	journal := &nodev1.RuntimeJournalIdentity{RuntimeId: "xray", CoreId: "xray", StateJournalId: uuid.NewString(), CreditJournalId: uuid.NewString()}
	if err := s.BindRuntimeJournals([]*nodev1.RuntimeJournalIdentity{journal}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path, id)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.BindRuntimeJournals([]*nodev1.RuntimeJournalIdentity{journal}); err != nil {
		t.Fatal(err)
	}
	journal.StateJournalId = uuid.NewString()
	journal.CreditJournalId = uuid.NewString()
	if err := s.BindRuntimeJournals([]*nodev1.RuntimeJournalIdentity{journal}); !errors.Is(err, ErrIdentity) {
		t.Fatalf("replacement journals accepted: %v", err)
	}
}
