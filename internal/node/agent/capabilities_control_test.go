package agent

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/authority"
	"github.com/Arman2122/p-ui/internal/node/compatibility"
	"github.com/Arman2122/p-ui/internal/node/state"
)

type inventoryMaster struct {
	nodev1.UnimplementedAuthorityServiceServer
	nodev1.UnimplementedControlServiceServer
	id         state.Identity
	hellos     chan *nodev1.NodeHello
	heartbeats chan struct{}
	generation atomic.Uint64
}

func (*inventoryMaster) RegisterJournals(_ context.Context, request *nodev1.JournalRegistration) (*nodev1.JournalRegistrationReceipt, error) {
	_, digest, err := authority.NormalizeRegistration(request)
	return &nodev1.JournalRegistrationReceipt{AgentStateJournalId: request.AgentStateJournalId, RegistrationSha256: digest}, err
}

func (m *inventoryMaster) Connect(stream grpc.BidiStreamingServer[nodev1.NodeMessage, nodev1.MasterMessage]) error {
	message, err := stream.Recv()
	if err != nil {
		return err
	}
	m.hellos <- message.GetHello()
	if err := stream.Send(&nodev1.MasterMessage{Payload: &nodev1.MasterMessage_Authority{Authority: &nodev1.SessionAuthority{
		NodeId: m.id.NodeID, ControlPlaneId: m.id.ControlPlaneID, Generation: m.generation.Add(1), ProtocolVersion: compatibility.Protocol,
	}}}); err != nil {
		return err
	}
	for {
		message, err := stream.Recv()
		if err != nil {
			return err
		}
		if message.GetHeartbeat() != nil {
			select {
			case m.heartbeats <- struct{}{}:
			default:
			}
		}
	}
}

type inventoryApplier struct{ local *LocalRuntimes }

func (a inventoryApplier) Apply(ctx context.Context, deployment *nodev1.Deployment) error {
	return a.local.Apply(ctx, deployment)
}

func (a inventoryApplier) Journals(ctx context.Context) ([]*nodev1.RuntimeJournalIdentity, error) {
	return a.local.Journals(ctx)
}

func (a inventoryApplier) Capabilities(ctx context.Context) []*nodev1.CoreCapability {
	return a.local.Capabilities(ctx)
}

func inventoryTLS(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	certificate := &x509.Certificate{
		SerialNumber: big.NewInt(1), IsCA: true, BasicConstraintsValid: true,
		KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, DNSNames: []string{"inventory.test"},
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour),
	}
	raw, err := x509.CreateCertificate(rand.Reader, certificate, certificate, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(raw)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(parsed)
	pair := tls.Certificate{Certificate: [][]byte{raw}, PrivateKey: key}
	return &tls.Config{MinVersion: tls.VersionTLS13, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool, Certificates: []tls.Certificate{pair}},
		&tls.Config{MinVersion: tls.VersionTLS13, RootCAs: pool, ServerName: "inventory.test", Certificates: []tls.Certificate{pair}}
}

func TestControlRefreshesHelloWhenAuthenticatedInventoryChanges(t *testing.T) {
	local := inventoryLocal(t)
	engine := addInventoryWorker(t, local, "custom-xray", "xray", inventoryCapability("xray", "vless"), nil)
	master := &inventoryMaster{id: local.store.Identity(), hellos: make(chan *nodev1.NodeHello, 2), heartbeats: make(chan struct{}, 1)}
	serverTLS, clientTLS := inventoryTLS(t)
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(serverTLS)))
	nodev1.RegisterAuthorityServiceServer(server, master)
	nodev1.RegisterControlServiceServer(server, master)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close(); <-served })
	agent, err := New(local.store, listener.Addr().String(), clientTLS, inventoryApplier{local})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- agent.Control(ctx) }()
	finished := false
	defer func() {
		cancel()
		if !finished {
			<-done
		}
	}()
	var first *nodev1.NodeHello
	select {
	case first = <-master.hellos:
	case <-ctx.Done():
		t.Fatal("control did not publish initial inventory")
	}
	if first == nil || len(first.Capabilities) != 1 || !proto.Equal(first.Capabilities[0], engine.value.Load()) {
		t.Fatalf("wire Hello omitted verified inventory: %+v", first)
	}
	changed := proto.Clone(engine.value.Load()).(*nodev1.CoreCapability)
	changed.Version = "sha256:" + strings.Repeat("b", 64)
	engine.value.Store(changed)
	select {
	case err := <-done:
		finished = true
		if err == nil || ctx.Err() != nil {
			t.Fatalf("control did not reconnect for changed inventory: %v", err)
		}
	case <-master.heartbeats:
		t.Fatal("old capability session sent a heartbeat after inventory changed")
	case <-ctx.Done():
		t.Fatal("changed inventory did not end control session")
	}
	secondContext, stop := context.WithCancel(t.Context())
	secondDone := make(chan error, 1)
	go func() { secondDone <- agent.Control(secondContext) }()
	defer func() { stop(); <-secondDone }()
	select {
	case next := <-master.hellos:
		if next == nil || len(next.Capabilities) != 1 || !proto.Equal(next.Capabilities[0], changed) {
			t.Fatalf("reconnected Hello retained old build: %+v", next)
		}
	case <-ctx.Done():
		t.Fatal("control did not publish refreshed inventory")
	}
}
