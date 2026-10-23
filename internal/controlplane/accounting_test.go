package controlplane

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"google.golang.org/protobuf/proto"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := os.Getenv("PUI_DB_DSN")
	if dsn == "" {
		t.Skip("PUI_DB_DSN required for live PostgreSQL accounting tests")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin := sql.OpenDB(stdlib.GetConnector(*cfg))
	schema := "node_cp_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(context.Background(), "CREATE SCHEMA "+schema); err != nil {
		_ = admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE")
		_ = admin.Close()
	})
	cfg.RuntimeParams["search_path"] = schema
	pool := sql.OpenDB(stdlib.GetConnector(*cfg))
	pool.SetMaxOpenConns(12)
	t.Cleanup(func() { _ = pool.Close() })
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: pool}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&model.ClientRecord{}, &model.FleetClient{}, &model.FleetTerm{}, &model.FleetTermUsage{}, &model.FleetTermUsageDay{}, &model.FleetGrantStop{}, &model.FleetPlacement{}, &model.FleetPlacementInbound{}, &model.FleetBudgetScope{}, &model.FleetNode{}, &model.FleetDeployment{}, &model.FleetBudget{}, &model.FleetGrant{},
		&model.FleetGrantBudget{}, &model.FleetUsageCursor{}, &model.FleetMeter{}, &model.FleetOperation{}, &model.FleetEnrollment{}, &model.FleetCredential{}, &model.FleetFence{}, &model.FleetRateBudget{}, &model.FleetRateShare{}, &model.FleetAuthority{}, &model.FleetRecoveryChallenge{}, &model.FleetNodeObservation{}, &model.FleetNodeJournal{}, &model.FleetRuntimeJournal{}, &model.FleetGrantAuthority{}, &model.FleetCreditUpdate{}, &model.FleetCreditCandidate{}, &model.FleetCreditNodeQueue{},
		&model.FleetAdmissionDemand{}, &model.FleetAdmissionNodeQueue{}, &model.FleetRateReconciliation{}, &model.FleetPublishedRuntime{}, &model.FleetPublishedClientAdmission{}, &model.FleetPublishedAdmissionBinding{}, &model.FleetAuthorityUpdate{},
		&model.FleetNetworkCompilation{}, &model.FleetNetworkPublication{}, &model.FleetPublishedProviderBinding{}, &model.FleetNodeCapabilitySnapshot{}, &model.FleetPlacementFanout{}, &model.FleetNodePlacementPolicy{}, &model.FleetPlacementRule{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func setupLedger(t *testing.T) (*Store, *gorm.DB, []identity.Principal, string, string, []string) {
	t.Helper()
	db := testDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	nodes := []identity.Principal{
		{NodeID: uuid.NewString(), InstallationID: uuid.NewString()},
		{NodeID: uuid.NewString(), InstallationID: uuid.NewString()},
	}
	for _, n := range nodes {
		if err := db.Create(&model.FleetNode{ID: n.NodeID, InstallationID: n.InstallationID, Name: n.NodeID, Enabled: true}).Error; err != nil {
			t.Fatal(err)
		}
	}
	scopes := []string{uuid.NewString(), uuid.NewString()}
	for _, id := range scopes {
		if err := db.Create(&model.FleetBudget{ID: id, LimitBytes: 20 << 30}).Error; err != nil {
			t.Fatal(err)
		}
	}
	slices.Sort(scopes)
	return store, db, nodes, uuid.NewString(), uuid.NewString(), scopes
}

func reserve(t *testing.T, s *Store, node identity.Principal, client, term string, scopes []string, amount int64, runtimes ...string) ReserveRequest {
	t.Helper()
	runtime := "xray"
	if len(runtimes) != 0 {
		runtime = runtimes[0]
	}
	bootstrapTestRuntime(t, s, node, runtime)
	request := ReserveRequest{
		OperationID: uuid.NewString(), GrantID: uuid.NewString(), NodeID: node.NodeID,
		InstallationID: node.InstallationID, ClientID: client, TermID: term, BudgetIDs: scopes, Bytes: amount,
		RuntimeID: runtime,
	}
	if _, err := s.Reserve(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	return request
}

func batch(node identity.Principal, grant ReserveRequest, sequence, up, down uint64) *nodev1.UsageBatch {
	return &nodev1.UsageBatch{
		NodeId: node.NodeID, InstallationId: node.InstallationID, Sequence: sequence,
		Checkpoints: []*nodev1.UsageCheckpoint{{
			GrantId: grant.GrantID, ClientId: grant.ClientID, TermId: grant.TermID,
			MeterId: grant.RuntimeID, MeterIncarnation: grant.GrantID, Uploaded: up, Downloaded: down, ObservedUnixMilli: 1,
		}},
	}
}

func budget(t *testing.T, db *gorm.DB, id string, used, reserved int64) {
	t.Helper()
	var b model.FleetBudget
	if err := db.First(&b, "id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	if b.UsedBytes != used || b.ReservedBytes != reserved {
		t.Fatalf("budget=%+v, want used=%d reserved=%d", b, used, reserved)
	}
}

func TestUsageBudgetLockExceedsPostgresBindLimit(t *testing.T) {
	db := testDB(t)
	ids := make([]string, 1<<16)
	rows := make([]model.FleetBudget, len(ids))
	for i := range rows {
		ids[i] = uuid.NewString()
		rows[i] = model.FleetBudget{ID: ids[i], LimitBytes: 1, ReservedBytes: 1}
	}
	if err := db.CreateInBatches(rows, 1000).Error; err != nil {
		t.Fatal(err)
	}
	slices.Sort(ids)
	if err := db.Transaction(func(tx *gorm.DB) error {
		locked, err := lockBudgets(tx, ids)
		if err != nil {
			return err
		}
		if len(locked) != len(ids) {
			t.Fatalf("locked=%d want=%d", len(locked), len(ids))
		}
		for _, id := range ids {
			if row := locked[id]; row == nil || row.ID != id || row.ReservedBytes != 1 || row.LimitBytes != 1 {
				t.Fatalf("missing or changed budget %s: %+v", id, row)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("valid usage batch exceeds database query limit: %v", err)
	}
}

func TestTwoNodeSharedQuotaPostgres(t *testing.T) {
	s, db, nodes, client, term, scopes := setupLedger(t)
	a := reserve(t, s, nodes[0], client, term, scopes, 8<<30)
	b := reserve(t, s, nodes[1], client, term, scopes, 8<<30)
	first := batch(nodes[0], a, 1, 1<<30, 5<<30)
	for range 2 {
		receipt, err := s.Ingest(context.Background(), nodes[0], first)
		if err != nil || receipt.DurableSequence != 1 || receipt.AccountedSequence != 1 {
			t.Fatalf("receipt=%v err=%v", receipt, err)
		}
	}
	if _, err := s.Ingest(context.Background(), nodes[1], batch(nodes[1], b, 1, 1<<30, 2<<30)); err != nil {
		t.Fatal(err)
	}
	for _, id := range scopes {
		budget(t, db, id, 9<<30, 7<<30)
	}
	a.OperationID = uuid.NewString()
	a.Bytes = 4 << 30
	topUp, err := s.Reserve(context.Background(), a)
	if err != nil || topUp.IssuedBytes != 12<<30 || topUp.Revision != 2 {
		t.Fatalf("top-up=%+v err=%v", topUp, err)
	}
	if _, err := s.Reserve(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	for _, id := range scopes {
		budget(t, db, id, 9<<30, 11<<30)
	}
	if _, err := s.Ingest(context.Background(), nodes[0], batch(nodes[0], a, 2, 3<<30, 9<<30)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Ingest(context.Background(), nodes[1], batch(nodes[1], b, 2, 3<<30, 5<<30)); err != nil {
		t.Fatal(err)
	}
	for _, id := range scopes {
		budget(t, db, id, 20<<30, 0)
	}
	a.OperationID = uuid.NewString()
	a.Bytes = 1
	if _, err := s.Reserve(context.Background(), a); !errors.Is(err, ErrBudget) {
		t.Fatalf("overspend grant=%v", err)
	}
}

func TestConcurrentReservationCannotMultiplyBudgetPostgres(t *testing.T) {
	s, db, nodes, client, term, scopes := setupLedger(t)
	for _, node := range nodes {
		bootstrapTestRuntime(t, s, node, "xray")
	}
	var accepted atomic.Int64
	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := range 32 {
		wg.Go(func() {
			n := nodes[i%len(nodes)]
			_, err := s.Reserve(context.Background(), ReserveRequest{
				OperationID: uuid.NewString(), GrantID: uuid.NewString(),
				RuntimeID: "xray",
				NodeID:    n.NodeID, InstallationID: n.InstallationID, ClientID: client, TermID: term, BudgetIDs: scopes, Bytes: 1 << 30,
			})
			if err == nil {
				accepted.Add(1)
			} else if !errors.Is(err, ErrBudget) {
				errs <- err
			}
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if accepted.Load() != 20 {
		t.Fatalf("accepted=%d, want 20", accepted.Load())
	}
	for _, id := range scopes {
		budget(t, db, id, 0, 20<<30)
	}
}

func TestRejectedBatchPreservesLedgerAndCursorPostgres(t *testing.T) {
	s, db, nodes, client, term, scopes := setupLedger(t)
	a := reserve(t, s, nodes[0], client, term, scopes, 1000)
	first := batch(nodes[0], a, 1, 100, 200)
	if _, err := s.Ingest(context.Background(), nodes[0], first); err != nil {
		t.Fatal(err)
	}
	conflict := proto.Clone(first).(*nodev1.UsageBatch)
	conflict.Checkpoints[0].Uploaded++
	if _, err := s.Ingest(context.Background(), nodes[0], conflict); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflict=%v", err)
	}
	bad := batch(nodes[0], a, 2, 200, 300)
	bad.Checkpoints = append(bad.Checkpoints, &nodev1.UsageCheckpoint{
		GrantId: a.GrantID, ClientId: uuid.NewString(),
		TermId: term, MeterId: "other", MeterIncarnation: a.GrantID, Uploaded: 1, ObservedUnixMilli: 1,
	})
	if _, err := s.Ingest(context.Background(), nodes[0], bad); !errors.Is(err, ErrScope) {
		t.Fatalf("scope=%v", err)
	}
	if _, err := s.Ingest(context.Background(), nodes[1], batch(nodes[0], a, 2, 200, 300)); !errors.Is(err, ErrScope) {
		t.Fatalf("peer spoof=%v", err)
	}
	for _, id := range scopes {
		budget(t, db, id, 300, 700)
	}
	valid := batch(nodes[0], a, 2, 150, 250)
	if _, err := s.Ingest(context.Background(), nodes[0], valid); err != nil {
		t.Fatal(err)
	}
	for _, id := range scopes {
		budget(t, db, id, 400, 600)
	}
}
