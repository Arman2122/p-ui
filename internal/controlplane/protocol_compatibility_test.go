package controlplane

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/agent"
	"github.com/Arman2122/p-ui/internal/node/identity"
	"github.com/Arman2122/p-ui/internal/node/state"
)

type incompatibleMaster struct {
	nodev1.UnimplementedControlServiceServer
	principal identity.Principal
	version   uint32
	omit      bool
}

func (m *incompatibleMaster) Connect(stream grpc.BidiStreamingServer[nodev1.NodeMessage, nodev1.MasterMessage]) error {
	if _, err := stream.Recv(); err != nil {
		return err
	}
	if !m.omit {
		if err := stream.Send(&nodev1.MasterMessage{Payload: &nodev1.MasterMessage_Authority{Authority: &nodev1.SessionAuthority{NodeId: m.principal.NodeID, ControlPlaneId: m.principal.ControlPlaneID, Generation: 1, ProtocolVersion: m.version}}}); err != nil {
			return err
		}
	}
	payload := []byte("must not apply")
	digest := sha256.Sum256(payload)
	if err := stream.Send(&nodev1.MasterMessage{Payload: &nodev1.MasterMessage_Deployment{Deployment: &nodev1.Deployment{NodeId: m.principal.NodeID, ControlPlaneId: m.principal.ControlPlaneID, AuthorityGeneration: 1, Revision: 1, Payload: payload, PayloadSha256: digest[:]}}}); err != nil {
		return err
	}
	<-stream.Context().Done()
	return stream.Context().Err()
}

func TestAgentRefusesIncompatibleMasterBeforePersistingAuthorityPostgres(t *testing.T) {
	for _, mode := range []string{"old", "future", "missing"} {
		t.Run(mode, func(t *testing.T) {
			store, _, nodes, _, _, _ := setupLedger(t)
			p := nodes[0]
			p.ControlPlaneID = uuid.NewString()
			serverTLS, clientTLS, _, _ := tlsPair(t, p)
			serverTLS.ClientAuth = tls.RequireAndVerifyClientCert
			registerIssuedTestCredential(t, store, p, clientTLS)
			fake := &incompatibleMaster{principal: p, version: 1, omit: mode == "missing"}
			if mode == "future" {
				fake.version = 3
			}
			server := grpc.NewServer(grpc.Creds(credentials.NewTLS(serverTLS)))
			nodev1.RegisterControlServiceServer(server, fake)
			nodev1.RegisterAuthorityServiceServer(server, &authorityService{store: store, plane: p.ControlPlaneID})
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			go func() { done <- server.Serve(listener) }()
			t.Cleanup(func() { server.Stop(); _ = listener.Close(); <-done })
			local, err := state.Open(filepath.Join(t.TempDir(), "agent.db"), state.Identity{NodeID: p.NodeID, InstallationID: p.InstallationID, ControlPlaneID: p.ControlPlaneID})
			if err != nil {
				t.Fatal(err)
			}
			defer local.Close()
			proceed := make(chan struct{})
			close(proceed)
			applier := &fileApplier{journal: &nodev1.RuntimeJournalIdentity{RuntimeId: "xray", CoreId: "xray", StateJournalId: uuid.NewString(), CreditJournalId: uuid.NewString()}, path: filepath.Join(t.TempDir(), "active"), entered: make(chan uint64, 1), proceed: proceed}
			node, err := agent.New(local, listener.Addr().String(), clientTLS, applier)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			err = node.Control(ctx)
			if err == nil || !strings.Contains(err.Error(), "master node protocol") {
				t.Fatalf("incompatible master response accepted: %v", err)
			}
			cursors, err := local.Cursors()
			if err != nil || cursors.Authority != 0 || cursors.Desired != 0 || cursors.Applied != 0 {
				t.Fatalf("incompatible master changed durable authority: %+v %v", cursors, err)
			}
			if _, err := os.Stat(applier.path); !os.IsNotExist(err) {
				t.Fatalf("incompatible master reached apply: %v", err)
			}
		})
	}
}
