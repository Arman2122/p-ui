package controlplane

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"gorm.io/gorm"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

func TestActivationDispatchUsesAuthenticatedSessionAndBoundDatabase(t *testing.T) {
	store, db, nodes, clientID, termID, _ := setupLedger(t)
	principal := nodes[0]
	principal.ControlPlaneID = uuid.NewString()
	var called atomic.Int32
	service, err := NewService(store, principal.ControlPlaneID, func(ctx context.Context, bound *gorm.DB, peer identity.Principal, generation uint64, request *nodev1.ActivationRequest) (*nodev1.ActivationResult, error) {
		called.Add(1)
		if bound != db || peer.Require(principal.NodeID, principal.InstallationID) != nil || peer.ControlPlaneID != principal.ControlPlaneID || peer.Serial == "" || generation != 1 || request.CredentialGeneration != 7 {
			return nil, ErrScope
		}
		if verified, err := identity.FromContext(ctx, principal.ControlPlaneID); err != nil || verified != peer {
			return nil, ErrScope
		}
		if request.ClientId != clientID {
			return nil, ErrScope
		}
		return &nodev1.ActivationResult{RequestId: request.RequestId, ClientId: clientID, TermId: termID, ExpiresUnixMilli: time.Now().Add(time.Hour).UnixMilli()}, nil
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
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	stream, err := nodev1.NewControlServiceClient(connection).Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&nodev1.NodeMessage{Payload: &nodev1.NodeMessage_Hello{Hello: journalHello(t, store, principal)}}); err != nil {
		t.Fatal(err)
	}
	message, err := stream.Recv()
	if err != nil || message.GetAuthority() == nil || message.GetAuthority().Generation != 1 {
		t.Fatalf("session authority: %v %v", message, err)
	}
	for _, test := range []struct {
		name, client, want string
		deadline           time.Time
		calls              int32
	}{
		{"approved", clientID, "", time.Now().Add(10 * time.Second), 1},
		{"expired demand", clientID, "INVALID_REQUEST", time.Now().Add(-time.Second), 1},
		{"foreign client", uuid.NewString(), "NOT_AUTHORIZED", time.Now().Add(10 * time.Second), 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := &nodev1.ActivationRequest{
				RequestId: uuid.NewString(), ClientId: test.client, CredentialGeneration: 7, DeadlineUnixMilli: test.deadline.UnixMilli(),
				RuntimeId: "xray", DeploymentRevision: 1, PlacementId: uuid.NewString(), PlacementRevision: 1, InboundInstanceId: uuid.NewString(),
			}
			if err := stream.Send(&nodev1.NodeMessage{Payload: &nodev1.NodeMessage_Activation{Activation: request}}); err != nil {
				t.Fatal(err)
			}
			message, err := stream.Recv()
			if err != nil || message.GetActivation() == nil {
				t.Fatalf("activation response: %v %v", message, err)
			}
			result := message.GetActivation()
			if result.ErrorCode != test.want || result.RequestId != request.RequestId || called.Load() != test.calls {
				t.Fatalf("activation=%+v calls=%d", result, called.Load())
			}
			if test.want == "" && result.TermId != termID || test.want != "" && result.TermId != "" {
				t.Fatalf("incorrect term authority: %+v", result)
			}
		})
	}
	if err := db.Model(&model.FleetNode{}).Where("id = ?", principal.NodeID).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	request := &nodev1.ActivationRequest{
		RequestId: uuid.NewString(), ClientId: clientID, CredentialGeneration: 7, DeadlineUnixMilli: time.Now().Add(5 * time.Second).UnixMilli(),
		RuntimeId: "xray", DeploymentRevision: 1, PlacementId: uuid.NewString(), PlacementRevision: 1, InboundInstanceId: uuid.NewString(),
	}
	result := service.activate(ctx, principal, 1, request)
	if result.ErrorCode != "NOT_AUTHORIZED" || called.Load() != 2 {
		t.Fatalf("revoked session reached activation: %+v calls=%d", result, called.Load())
	}
}
