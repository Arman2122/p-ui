package controlplane

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"gorm.io/gorm"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

func TestSlowActivationDoesNotBlockConfigurationDelivery(t *testing.T) {
	store, _, nodes, clientID, termID, _ := setupLedger(t)
	principal := nodes[0]
	principal.ControlPlaneID = uuid.NewString()
	entered := make(chan struct{}, 4)
	release := make(chan struct{})
	service, err := NewService(store, principal.ControlPlaneID, func(ctx context.Context, _ *gorm.DB, _ identity.Principal, _ uint64, request *nodev1.ActivationRequest) (*nodev1.ActivationResult, error) {
		entered <- struct{}{}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-release:
		}
		return &nodev1.ActivationResult{RequestId: request.RequestId, ClientId: request.ClientId, TermId: termID}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	serverTLS, clientTLS, _, _ := tlsPair(t, principal)
	registerIssuedTestCredential(t, store, principal, clientTLS)
	server, err := NewGRPCServer(service, serverTLS)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close(); <-done })
	connection, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(credentials.NewTLS(clientTLS)))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	stream, err := nodev1.NewControlServiceClient(connection).Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&nodev1.NodeMessage{Payload: &nodev1.NodeMessage_Hello{Hello: journalHello(t, store, principal)}}); err != nil {
		t.Fatal(err)
	}
	if message, err := stream.Recv(); err != nil || message.GetAuthority() == nil {
		t.Fatalf("session=%v %v", message, err)
	}
	request := &nodev1.ActivationRequest{RequestId: uuid.NewString(), ClientId: clientID, CredentialGeneration: 1, DeadlineUnixMilli: time.Now().Add(10 * time.Second).UnixMilli(), RuntimeId: "xray", DeploymentRevision: 1, PlacementId: uuid.NewString(), PlacementRevision: 1, InboundInstanceId: uuid.NewString()}
	if err := stream.Send(&nodev1.NodeMessage{Payload: &nodev1.NodeMessage_Activation{Activation: request}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("activation handler not entered")
	}
	if revision, err := service.Publish(ctx, principal.NodeID, []byte(`{"configuration":"unblocks-control"}`)); err != nil || revision != 1 {
		t.Fatalf("publish=%d %v", revision, err)
	}
	message, err := stream.Recv()
	if err != nil || message.GetDeployment() == nil || message.GetDeployment().Revision != 1 {
		t.Fatalf("activation blocked deployment=%v %v", message, err)
	}
	close(release)
	message, err = stream.Recv()
	if err != nil || message.GetActivation() == nil || message.GetActivation().RequestId != request.RequestId || message.GetActivation().TermId != termID {
		t.Fatalf("activation result=%v %v", message, err)
	}
}
