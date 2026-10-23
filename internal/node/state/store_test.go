package state

import (
	"crypto/sha256"
	"errors"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
)

func identity() Identity {
	return Identity{NodeID: uuid.NewString(), InstallationID: uuid.NewString(), ControlPlaneID: uuid.NewString()}
}

func openStore(t *testing.T, id Identity) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "state.db"), id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func deployment(id Identity, revision, generation uint64, payload string) *nodev1.Deployment {
	digest := sha256.Sum256([]byte(payload))
	return &nodev1.Deployment{
		NodeId: id.NodeID, ControlPlaneId: id.ControlPlaneID,
		AuthorityGeneration: generation, Revision: revision, Payload: []byte(payload), PayloadSha256: digest[:],
	}
}

func checkpoint() *nodev1.UsageCheckpoint {
	return &nodev1.UsageCheckpoint{
		GrantId: uuid.NewString(), ClientId: uuid.NewString(), TermId: uuid.NewString(),
		MeterId: "native-client-meter", MeterIncarnation: uuid.NewString(), Uploaded: 100, Downloaded: 200, ObservedUnixMilli: 1,
	}
}

func TestDeploymentSurvivesReopenAndRejectsStaleAuthority(t *testing.T) {
	id := identity()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path, id)
	if err != nil {
		t.Fatal(err)
	}
	d := deployment(id, 1, 1, "first")
	if err := s.Receive(d); err != nil {
		t.Fatal(err)
	}
	c, err := s.Cursors()
	if err != nil || c.Applied != 0 || c.Desired != 1 {
		t.Fatalf("received cursors=%+v err=%v", c, err)
	}
	if err := s.Applied(1, 1, d.PayloadSha256); err != nil {
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
	got, err := s.Desired()
	if err != nil || !proto.Equal(got, d) {
		t.Fatalf("desired=%v err=%v", got, err)
	}
	newer := deployment(id, 2, 2, "second")
	if err := s.Receive(newer); err != nil {
		t.Fatal(err)
	}
	if err := s.Receive(deployment(id, 3, 1, "stale-worker")); !errors.Is(err, ErrRevision) {
		t.Fatalf("stale receive=%v", err)
	}
	if err := s.Applied(1, 1, d.PayloadSha256); !errors.Is(err, ErrRevision) {
		t.Fatalf("stale applied=%v", err)
	}
	c, err = s.Cursors()
	if err != nil || c.Desired != 2 || c.Applied != 1 || c.Authority != 2 {
		t.Fatalf("cursors=%+v err=%v", c, err)
	}
}

func TestDeploymentIdentityIntegrityAndConflicts(t *testing.T) {
	id := identity()
	s := openStore(t, id)
	d := deployment(id, 2, 1, "good")
	if err := s.Receive(d); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*nodev1.Deployment)
		want   error
	}{
		{"wrong-node", func(d *nodev1.Deployment) { d.NodeId = uuid.NewString() }, ErrIdentity},
		{"wrong-plane", func(d *nodev1.Deployment) { d.ControlPlaneId = uuid.NewString() }, ErrIdentity},
		{"bad-digest", func(d *nodev1.Deployment) { d.Payload = []byte("tampered") }, ErrDigest},
		{"conflict", func(d *nodev1.Deployment) {
			d.Payload = []byte("new")
			sum := sha256.Sum256(d.Payload)
			d.PayloadSha256 = sum[:]
		}, ErrRevision},
		{"old-revision", func(d *nodev1.Deployment) { d.Revision = 1 }, ErrRevision},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := proto.Clone(d).(*nodev1.Deployment)
			tc.change(bad)
			if err := s.Receive(bad); !errors.Is(err, tc.want) {
				t.Fatalf("receive=%v, want %v", err, tc.want)
			}
		})
	}
	got, err := s.Desired()
	if err != nil || !proto.Equal(got, d) {
		t.Fatalf("changed desired=%v err=%v", got, err)
	}
}

func TestStoreCannotBeReboundToAnotherInstallation(t *testing.T) {
	id := identity()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	id.InstallationID = uuid.NewString()
	if _, err := Open(path, id); !errors.Is(err, ErrIdentity) {
		t.Fatalf("rebind=%v", err)
	}
}

func TestUsageReplayAndReceiptsSurviveReopen(t *testing.T) {
	id := identity()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := Open(path, id)
	if err != nil {
		t.Fatal(err)
	}
	c := checkpoint()
	for i := uint64(1); i <= 3; i++ {
		c.Uploaded = i * 100
		seq, err := s.AppendUsage([]*nodev1.UsageCheckpoint{proto.Clone(c).(*nodev1.UsageCheckpoint)})
		if err != nil || seq != i {
			t.Fatalf("append=(%d,%v), want %d", seq, err, i)
		}
		if _, err := s.PendingUsage(10); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(path, id)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pending, err := s.PendingUsage(10)
	if err != nil || len(pending) != 3 {
		t.Fatalf("pending=%v err=%v", pending, err)
	}
	for i, batch := range pending {
		if batch.Sequence != uint64(i+1) || batch.Checkpoints[0].Uploaded != uint64(i+1)*100 {
			t.Fatalf("batch=%v", batch)
		}
	}
	receipt := &nodev1.UsageReceipt{DurableSequence: 2, AccountedSequence: 1}
	if err := s.Acknowledge(receipt); err != nil {
		t.Fatal(err)
	}
	if err := s.Acknowledge(receipt); err != nil {
		t.Fatal(err)
	}
	if err := s.Acknowledge(&nodev1.UsageReceipt{DurableSequence: 1, AccountedSequence: 1}); err != nil {
		t.Fatal(err)
	}
	pending, err = s.PendingUsage(10)
	if err != nil || len(pending) != 1 || pending[0].Sequence != 3 {
		t.Fatalf("pending=%v err=%v", pending, err)
	}
	if err := s.Acknowledge(&nodev1.UsageReceipt{DurableSequence: 4, AccountedSequence: 4}); !errors.Is(err, ErrUsage) {
		t.Fatalf("future ack=%v", err)
	}
	cursors, err := s.Cursors()
	if err != nil || cursors.DurableUsage != 2 || cursors.AccountedUsage != 1 {
		t.Fatalf("cursors=%+v err=%v", cursors, err)
	}
}

func TestInvalidUsageBatchRollsBackMetersAndSequence(t *testing.T) {
	s := openStore(t, identity())
	first, second := checkpoint(), checkpoint()
	if _, err := s.AppendUsage([]*nodev1.UsageCheckpoint{first, second}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PendingUsage(10); err != nil {
		t.Fatal(err)
	}
	first.Uploaded = 200
	second.Uploaded = 99
	if _, err := s.AppendUsage([]*nodev1.UsageCheckpoint{first, second}); !errors.Is(err, ErrUsage) {
		t.Fatalf("regression=%v", err)
	}
	first.Uploaded = 150
	sequence, err := s.AppendUsage([]*nodev1.UsageCheckpoint{first})
	if err != nil || sequence != 2 {
		t.Fatalf("rollback append=(%d,%v)", sequence, err)
	}
	if _, err := s.AppendUsage([]*nodev1.UsageCheckpoint{first, first}); !errors.Is(err, ErrUsage) {
		t.Fatalf("duplicate meter=%v", err)
	}
	pending, err := s.PendingUsage(10)
	if err != nil || len(pending) != 2 || pending[1].Checkpoints[0].Uploaded != 150 {
		t.Fatalf("pending=%v err=%v", pending, err)
	}
}
