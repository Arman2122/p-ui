package controlplane

import (
	"context"
	"crypto/tls"
	"net"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/agent"
	"github.com/Arman2122/p-ui/internal/node/compatibility"
	"github.com/Arman2122/p-ui/internal/node/identity"
	"github.com/Arman2122/p-ui/internal/node/state"
)

type controlDemandSource struct {
	store            *state.Store
	journal          *nodev1.RuntimeJournalIdentity
	request          *nodev1.ActivationRequest
	started, stopped chan struct{}
	observed         chan uint64
	results          chan *nodev1.ActivationResult
}

func (*controlDemandSource) Apply(context.Context, *nodev1.Deployment) error { return nil }
func (s *controlDemandSource) Journals(context.Context) ([]*nodev1.RuntimeJournalIdentity, error) {
	return []*nodev1.RuntimeJournalIdentity{s.journal}, nil
}

func (s *controlDemandSource) RunActivations(ctx context.Context, send func(*nodev1.ActivationRequest) error) {
	defer close(s.stopped)
	cursors, err := s.store.Cursors()
	if err != nil {
		return
	}
	s.observed <- cursors.Authority
	close(s.started)
	request := proto.Clone(s.request).(*nodev1.ActivationRequest)
	request.DeadlineUnixMilli = time.Now().Add(5 * time.Second).UnixMilli()
	if send(request) != nil {
		return
	}
	<-ctx.Done()
}

func (s *controlDemandSource) ResolveActivation(ctx context.Context, result *nodev1.ActivationResult) {
	select {
	case s.results <- proto.Clone(result).(*nodev1.ActivationResult):
	case <-ctx.Done():
	}
}

type heldAuthorityStream struct {
	grpc.ServerStream
	waiting chan uint32
	release <-chan struct{}
}

func (s *heldAuthorityStream) SendMsg(message any) error {
	if frame, ok := message.(*nodev1.MasterMessage); ok && frame.GetAuthority() != nil {
		s.waiting <- frame.GetAuthority().ProtocolVersion
		select {
		case <-s.release:
		case <-s.Context().Done():
			return s.Context().Err()
		}
	}
	return s.ServerStream.SendMsg(message)
}

func TestAgentControlDeliversDemandAfterHandshakeAndJoinsCancellationPostgres(t *testing.T) {
	store, db, nodes, client, term, _ := setupLedger(t)
	p := nodes[0]
	p.ControlPlaneID = uuid.NewString()
	var calls atomic.Int32
	request := &nodev1.ActivationRequest{RequestId: uuid.NewString(), ClientId: client, CredentialGeneration: 7, RuntimeId: "xray", DeploymentRevision: 9, PlacementId: uuid.NewString(), PlacementRevision: 11, InboundInstanceId: uuid.NewString()}
	service, err := NewService(store, p.ControlPlaneID, func(ctx context.Context, bound *gorm.DB, peer identity.Principal, generation uint64, got *nodev1.ActivationRequest) (*nodev1.ActivationResult, error) {
		if bound != db || generation != 1 || peer.Require(p.NodeID, p.InstallationID) != nil || peer.Serial == "" || got.RequestId != request.RequestId || got.ClientId != client || got.CredentialGeneration != 7 || got.DeploymentRevision != 9 || got.PlacementId != request.PlacementId || got.PlacementRevision != 11 || got.InboundInstanceId != request.InboundInstanceId {
			return nil, ErrScope
		}
		if verified, err := identity.FromContext(ctx, p.ControlPlaneID); err != nil || verified != peer {
			return nil, ErrScope
		}
		var count int64
		if err := bound.WithContext(ctx).Model(&model.FleetNode{}).Where("id = ? AND installation_id = ?", p.NodeID, p.InstallationID).Count(&count).Error; err != nil || count != 1 {
			return nil, ErrScope
		}
		calls.Add(1)
		return &nodev1.ActivationResult{RequestId: got.RequestId, ClientId: got.ClientId, TermId: term, ExpiresUnixMilli: time.Now().Add(time.Hour).UnixMilli()}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	serverTLS, clientTLS, _, _ := tlsPair(t, p)
	serverTLS.ClientAuth = tls.RequireAndVerifyClientCert
	registerIssuedTestCredential(t, store, p, clientTLS)
	waiting, release := make(chan uint32, 1), make(chan struct{})
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(serverTLS)), grpc.StreamInterceptor(func(server any, stream grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		return handler(server, &heldAuthorityStream{ServerStream: stream, waiting: waiting, release: release})
	}))
	nodev1.RegisterControlServiceServer(server, service)
	nodev1.RegisterAuthorityServiceServer(server, &authorityService{store: store, plane: p.ControlPlaneID})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close(); <-served })
	local, err := state.Open(filepath.Join(t.TempDir(), "agent.db"), state.Identity{NodeID: p.NodeID, InstallationID: p.InstallationID, ControlPlaneID: p.ControlPlaneID})
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	source := &controlDemandSource{store: local, journal: &nodev1.RuntimeJournalIdentity{RuntimeId: "xray", CoreId: "xray", StateJournalId: uuid.NewString(), CreditJournalId: uuid.NewString()}, request: request, started: make(chan struct{}), stopped: make(chan struct{}), observed: make(chan uint64, 1), results: make(chan *nodev1.ActivationResult, 1)}
	node, err := agent.New(local, listener.Addr().String(), clientTLS, source)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- node.Control(ctx) }()
	select {
	case version := <-waiting:
		if version != compatibility.Protocol {
			t.Fatalf("authority protocol=%d", version)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("control handshake did not arrive")
	}
	select {
	case <-source.started:
		t.Fatal("queued attempt started before protocol authority")
	default:
	}
	close(release)
	select {
	case result := <-source.results:
		if result.RequestId != request.RequestId || result.ClientId != client || result.TermId != term || result.ErrorCode != "" || calls.Load() != 1 {
			t.Fatalf("authenticated result did not return to source: %+v calls=%d", result, calls.Load())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("activation result did not reach source")
	}
	if got := <-source.observed; got != 1 {
		t.Fatalf("source preceded durable authority: %d", got)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("control cancellation did not join activation source")
	}
	select {
	case <-source.stopped:
	default:
		t.Fatal("control returned before activation source stopped")
	}
}
