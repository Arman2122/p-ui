package controlplane

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
)

func TestTimeAuthorityRequiresCurrentAuthenticatedNodePostgres(t *testing.T) {
	store, db, nodes, _, _, _ := setupLedger(t)
	principal := nodes[0]
	principal.ControlPlaneID = uuid.NewString()
	service, err := NewService(store, principal.ControlPlaneID)
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
	client := nodev1.NewTimeServiceClient(connection)
	request := &nodev1.TimeRequest{NodeId: principal.NodeID, InstallationId: principal.InstallationID, Nonce: uuid.NewString()}
	before := time.Now().UnixMilli()
	reply, err := client.Synchronize(ctx, request)
	if err != nil || reply.Nonce != request.Nonce || reply.NodeId != principal.NodeID || reply.InstallationId != principal.InstallationID || reply.ServerUnixMilli < before || reply.ServerUnixMilli > time.Now().UnixMilli() {
		t.Fatalf("authenticated time response=%+v %v", reply, err)
	}
	request.InstallationId = uuid.NewString()
	if _, err := client.Synchronize(ctx, request); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("foreign installation time=%v", err)
	}
	request.InstallationId = principal.InstallationID
	_, replacementTLS, _, _ := tlsPair(t, principal)
	if err := db.Where("node_id = ?", principal.NodeID).Delete(&model.FleetCredential{}).Error; err != nil {
		t.Fatal(err)
	}
	registerIssuedTestCredential(t, store, principal, replacementTLS)
	if _, err := client.Synchronize(ctx, request); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("superseded certificate received time authority: %v", err)
	}
	var grants int64
	if err := db.Model(&model.FleetGrant{}).Count(&grants).Error; err != nil || grants != 0 {
		t.Fatalf("time RPC changed credit grants=%d %v", grants, err)
	}
}
