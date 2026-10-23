package controlplane

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/agent"
	"github.com/Arman2122/p-ui/internal/node/compatibility"
	"github.com/Arman2122/p-ui/internal/node/identity"
	"github.com/Arman2122/p-ui/internal/node/state"
)

func tlsPair(t *testing.T, p identity.Principal) (*tls.Config, *tls.Config, *Issuer, []byte) {
	t.Helper()
	now := time.Now()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "control-test"},
		IsCA: true, BasicConstraintsValid: true, MaxPathLenZero: true, KeyUsage: x509.KeyUsageCertSign,
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(24 * time.Hour),
	}
	raw, err := x509.CreateCertificate(rand.Reader, template, template, caKey.Public(), caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(raw)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca)
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, &x509.Certificate{
		SerialNumber: big.NewInt(2), DNSNames: []string{"control.test"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}, ca, serverKey.Public(), caKey)
	if err != nil {
		t.Fatal(err)
	}
	nodeKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, nodeKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := identity.Issue(ca, caKey, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr}), p, now)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(cert)
	return &tls.Config{MinVersion: tls.VersionTLS13, ClientCAs: roots, Certificates: []tls.Certificate{{Certificate: [][]byte{serverDER}, PrivateKey: serverKey}}},
		&tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: "control.test", Certificates: []tls.Certificate{{Certificate: [][]byte{block.Bytes}, PrivateKey: nodeKey}}},
		&Issuer{Certificate: ca, Signer: caKey, TrustBundle: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw})},
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr})
}

func registerIssuedTestCredential(t *testing.T, store *Store, principal identity.Principal, config *tls.Config) {
	t.Helper()
	leaf, err := x509.ParseCertificate(config.Certificates[0].Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	digest, err := identity.PublicKeyDigest(leaf.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	credential := model.FleetCredential{
		NodeID: principal.NodeID, InstallationID: principal.InstallationID, RequestID: uuid.NewString(),
		KeyDigest: digest, Certificate: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leaf.Raw}), IssuedUnixMilli: time.Now().Add(-2 * time.Minute).UnixMilli(),
	}
	if err := store.db.Create(&credential).Error; err != nil {
		t.Fatal(err)
	}
}

type fileApplier struct {
	journal      *nodev1.RuntimeJournalIdentity
	failRevision uint64
	failed       atomic.Bool
	path         string
	entered      chan uint64
	proceed      <-chan struct{}
}

func (a *fileApplier) Journals(context.Context) ([]*nodev1.RuntimeJournalIdentity, error) {
	return []*nodev1.RuntimeJournalIdentity{a.journal}, nil
}

func (a *fileApplier) Apply(ctx context.Context, d *nodev1.Deployment) error {
	select {
	case a.entered <- d.Revision:
	default:
	}
	select {
	case <-a.proceed:
	case <-ctx.Done():
		return ctx.Err()
	}
	if d.Revision == a.failRevision && !a.failed.Swap(true) {
		return errors.New("temporary local application failure")
	}
	return os.WriteFile(a.path, d.Payload, 0o600)
}

func await(t *testing.T, condition func() (bool, error)) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		ok, err := condition()
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("condition did not become true")
		case <-tick.C:
		}
	}
}

func TestAuthenticatedControlAndAccountingPostgres(t *testing.T) {
	store, db, nodes, client, term, scopes := setupLedger(t)
	p := nodes[0]
	p.ControlPlaneID = uuid.NewString()
	service, err := NewService(store, p.ControlPlaneID)
	if err != nil {
		t.Fatal(err)
	}
	serverTLS, clientTLS, _, _ := tlsPair(t, p)
	registerIssuedTestCredential(t, store, p, clientTLS)
	server, err := NewGRPCServer(service, serverTLS)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close(); <-serveDone })
	first := []byte("{\"configuration\":\"one\"}")
	second := []byte("{\"configuration\":\"two\"}")
	if revision, err := service.Publish(context.Background(), p.NodeID, first); err != nil || revision != 1 {
		t.Fatalf("publish=(%d,%v)", revision, err)
	}
	local, err := state.Open(filepath.Join(t.TempDir(), "node.db"), state.Identity{
		NodeID: p.NodeID, InstallationID: p.InstallationID, ControlPlaneID: p.ControlPlaneID,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = local.Close() })
	proceed := make(chan struct{})
	journal := &nodev1.RuntimeJournalIdentity{RuntimeId: "xray", CoreId: "xray", StateJournalId: uuid.NewString(), CreditJournalId: uuid.NewString()}
	if _, err := store.RegisterJournals(t.Context(), p, &nodev1.JournalRegistration{NodeId: p.NodeID, InstallationId: p.InstallationID, AgentStateJournalId: local.JournalID(), Runtimes: []*nodev1.RuntimeJournalIdentity{journal}}); err != nil {
		t.Fatal(err)
	}
	reservation := reserve(t, store, p, client, term, scopes, 1000)
	applier := &fileApplier{journal: journal, path: filepath.Join(t.TempDir(), "active.json"), entered: make(chan uint64, 4), proceed: proceed, failRevision: 2}
	node, err := agent.New(local, listener.Addr().String(), clientTLS, applier)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 2)
	go func() { done <- node.Control(ctx) }()
	go func() { done <- node.Accounting(ctx) }()
	t.Cleanup(func() {
		cancel()
		for range 2 {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("agent did not stop")
			}
		}
	})
	select {
	case revision := <-applier.entered:
		if revision != 1 {
			t.Fatalf("first revision=%d", revision)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("deployment did not reach node")
	}
	cursors, err := local.Cursors()
	if err != nil || cursors.Desired != 1 || cursors.Applied != 0 {
		t.Fatalf("received != applied: %+v %v", cursors, err)
	}
	var row model.FleetNode
	if err := db.First(&row, "id = ?", p.NodeID).Error; err != nil {
		t.Fatal(err)
	}
	if row.AppliedRevision != 0 {
		t.Fatalf("master prematurely marked applied: %d", row.AppliedRevision)
	}
	if revision, err := service.Publish(ctx, p.NodeID, second); err != nil || revision != 2 {
		t.Fatalf("second publish=(%d,%v)", revision, err)
	}
	close(proceed)
	await(t, func() (bool, error) {
		var row model.FleetNode
		err := db.First(&row, "id = ?", p.NodeID).Error
		return row.AppliedRevision == 2, err
	})
	got, err := os.ReadFile(applier.path)
	if err != nil || !bytes.Equal(got, second) {
		t.Fatalf("applied payload=%q err=%v", got, err)
	}
	checkpoint := batch(p, reservation, 1, 100, 200).Checkpoints[0]
	if sequence, err := local.AppendUsage([]*nodev1.UsageCheckpoint{checkpoint}); err != nil || sequence != 1 {
		t.Fatalf("append=(%d,%v)", sequence, err)
	}
	await(t, func() (bool, error) {
		c, err := local.Cursors()
		return c.DurableUsage == 1 && c.AccountedUsage == 1, err
	})
	for _, scope := range scopes {
		budget(t, db, scope, 300, 700)
	}
	pending, err := local.PendingUsage(16)
	if err != nil || len(pending) != 0 {
		t.Fatalf("acknowledged batch retained: %v %v", pending, err)
	}

	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(credentials.NewTLS(clientTLS)))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, version := range []uint32{0, 1, 3} {
		versionContext, versionCancel := context.WithTimeout(ctx, 3*time.Second)
		incompatible, err := nodev1.NewControlServiceClient(conn).Connect(versionContext)
		if err != nil {
			versionCancel()
			t.Fatal(err)
		}
		hello := journalHello(t, store, p)
		hello.ProtocolVersion = version
		if err := incompatible.Send(&nodev1.NodeMessage{Payload: &nodev1.NodeMessage_Hello{Hello: hello}}); err != nil {
			versionCancel()
			t.Fatal(err)
		}
		_, err = incompatible.Recv()
		versionCancel()
		if status.Code(err) != codes.PermissionDenied {
			t.Fatalf("unsupported protocol %d accepted: %v", version, err)
		}
	}
	badContext, badCancel := context.WithTimeout(ctx, 3*time.Second)
	defer badCancel()
	stream, err := nodev1.NewControlServiceClient(conn).Connect(badContext)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&nodev1.NodeMessage{Payload: &nodev1.NodeMessage_Hello{Hello: &nodev1.NodeHello{
		NodeId: nodes[1].NodeID, InstallationId: nodes[1].InstallationID, ProtocolVersion: compatibility.Protocol,
	}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("spoof status=%v", err)
	}
}
