package controlplane

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
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
	vault "github.com/Arman2122/p-ui/internal/node/credentials"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

type lostRenewResponse struct{ grpc.ClientConnInterface }

func (c lostRenewResponse) Invoke(ctx context.Context, method string, args, reply any, options ...grpc.CallOption) error {
	if err := c.ClientConnInterface.Invoke(ctx, method, args, reply, options...); err != nil {
		return err
	}
	return context.DeadlineExceeded
}

func TestCredentialRecoveryAndRotationPostgres(t *testing.T) {
	store, err := NewStore(testDB(t))
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := store.CreateEnrollment(t.Context(), "credential-test")
	if err != nil {
		t.Fatal(err)
	}
	p := identity.Principal{ControlPlaneID: uuid.NewString(), NodeID: enrollment.NodeID, InstallationID: enrollment.InstallationID}
	serverTLS, _, issuer, csr := tlsPair(t, p)
	handler, err := NewEnrollmentHandler(store, p.ControlPlaneID, issuer)
	if err != nil {
		t.Fatal(err)
	}
	var drop atomic.Bool
	drop.Store(true)
	bootstrap := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !drop.Swap(false) {
			handler.ServeHTTP(w, r)
			return
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, r)
		if recorder.Code != http.StatusOK {
			t.Errorf("enrollment before lost response status=%d", recorder.Code)
		}
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = connection.Close()
	}))
	bootstrap.TLS = serverTLS.Clone()
	bootstrap.StartTLS()
	defer bootstrap.Close()
	service, err := NewService(store, p.ControlPlaneID)
	if err != nil {
		t.Fatal(err)
	}
	server, err := NewGRPCServer(service, serverTLS, issuer)
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
	path := filepath.Join(t.TempDir(), "credentials.db")
	config := vault.Config{
		Identity: p, ControlAddress: listener.Addr().String(), ServerName: "control.test",
		EnrollmentURL: bootstrap.URL + "/enroll", TrustBundlePEM: issuer.TrustBundle,
	}
	local, err := vault.Open(path, &config)
	if err != nil {
		t.Fatal(err)
	}
	if err := local.Enroll(t.Context(), enrollment.Ticket); err == nil {
		t.Fatal("lost enrollment response was reported successful")
	}
	if err := local.Close(); err != nil {
		t.Fatal(err)
	}
	local, err = vault.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := local.Enroll(t.Context(), enrollment.Ticket); err != nil {
		_ = local.Close()
		t.Fatal(err)
	}
	clientTLS, err := local.TLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	original, err := clientTLS.GetClientCertificate(&tls.CertificateRequestInfo{})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := grpc.NewClient(config.ControlAddress, grpc.WithTransportCredentials(credentials.NewTLS(clientTLS)))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	oldControlContext, oldControlCancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer oldControlCancel()
	oldControl, err := nodev1.NewControlServiceClient(connection).Connect(oldControlContext)
	if err != nil {
		t.Fatal(err)
	}
	if err := oldControl.Send(&nodev1.NodeMessage{Payload: &nodev1.NodeMessage_Hello{Hello: journalHello(t, store, p)}}); err != nil {
		t.Fatal(err)
	}
	if authority, err := oldControl.Recv(); err != nil || authority.GetAuthority() == nil {
		t.Fatalf("initial enrolled control session: %v %v", authority, err)
	}
	if err := local.Renew(t.Context(), lostRenewResponse{connection}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lost renewal response=%v", err)
	}
	if err := local.Close(); err != nil {
		t.Fatal(err)
	}
	local, err = vault.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	if err := local.Renew(t.Context(), connection); err != nil {
		t.Fatal(err)
	}
	clientTLS, err = local.TLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	renewed, err := clientTLS.GetClientCertificate(&tls.CertificateRequestInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(original.Certificate[0], renewed.Certificate[0]) || bytes.Equal(original.Leaf.RawSubjectPublicKeyInfo, renewed.Leaf.RawSubjectPublicKeyInfo) {
		t.Fatal("renewal did not rotate certificate and key")
	}
	var saved model.FleetCredential
	if err := store.db.First(&saved, "node_id = ?", p.NodeID).Error; err != nil {
		t.Fatal(err)
	}
	_, err = nodev1.NewIdentityServiceClient(connection).Renew(t.Context(), &nodev1.RenewRequest{RequestId: saved.RequestID, CertificateRequestPem: csr})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("different key replay=%v", err)
	}
	if err := store.db.Model(&model.FleetCredential{}).Where("node_id = ?", p.NodeID).Update("issued_unix_milli", time.Now().Add(-2*time.Minute).UnixMilli()).Error; err != nil {
		t.Fatal(err)
	}
	staleTLS := clientTLS.Clone()
	staleTLS.GetClientCertificate = nil
	staleTLS.Certificates = []tls.Certificate{*original}
	stale, err := grpc.NewClient(config.ControlAddress, grpc.WithTransportCredentials(credentials.NewTLS(staleTLS)))
	if err != nil {
		t.Fatal(err)
	}
	defer stale.Close()
	registration := bootstrapTestRuntime(t, store, p, "xray")
	if _, err := nodev1.NewAuthorityServiceClient(stale).RegisterJournals(t.Context(), registration); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("superseded certificate registered journals: %v", err)
	}
	runtime := registration.Runtimes[0]
	if _, err := nodev1.NewAuthorityServiceClient(stale).Report(t.Context(), &nodev1.AuthorityReport{NodeId: p.NodeID, InstallationId: p.InstallationID, AgentStateJournalId: registration.AgentStateJournalId, RuntimeId: runtime.RuntimeId, StateJournalId: runtime.StateJournalId, CreditJournalId: runtime.CreditJournalId, Sequence: 1, ObservedUnixMilli: time.Now().UnixMilli()}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("superseded certificate reported authority: %v", err)
	}
	_, err = nodev1.NewIdentityServiceClient(stale).Renew(t.Context(), &nodev1.RenewRequest{RequestId: uuid.NewString(), CertificateRequestPem: csr})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("superseded certificate renewed a new key: %v", err)
	}
	staleContext, staleCancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer staleCancel()
	staleControl, err := nodev1.NewControlServiceClient(stale).Connect(staleContext)
	if err != nil {
		t.Fatal(err)
	}
	if err := staleControl.Send(&nodev1.NodeMessage{Payload: &nodev1.NodeMessage_Hello{Hello: journalHello(t, store, p)}}); err != nil {
		t.Fatal(err)
	}
	_, err = staleControl.Recv()
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("superseded certificate opened a control session: %v", err)
	}
	if err := oldControl.Send(&nodev1.NodeMessage{Payload: &nodev1.NodeMessage_Heartbeat{Heartbeat: &nodev1.Heartbeat{ObservedUnixMilli: time.Now().UnixMilli()}}}); err != nil {
		t.Fatal(err)
	}
	_, err = oldControl.Recv()
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("superseded live control session survived: %v", err)
	}
	budgetID := uuid.NewString()
	if err := store.db.Create(&model.FleetBudget{ID: budgetID, LimitBytes: 100}).Error; err != nil {
		t.Fatal(err)
	}
	grant := reserve(t, store, p, uuid.NewString(), uuid.NewString(), []string{budgetID}, 100)
	usage := batch(p, grant, 1, 1, 0)
	staleReport, err := nodev1.NewAccountingServiceClient(stale).Report(staleContext)
	if err != nil {
		t.Fatal(err)
	}
	if err := staleReport.Send(usage); err != nil {
		t.Fatal(err)
	}
	_, err = staleReport.Recv()
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("superseded certificate reported usage: %v", err)
	}
	budget(t, store.db, budgetID, 0, 100)
	currentConnection, err := grpc.NewClient(config.ControlAddress, grpc.WithTransportCredentials(credentials.NewTLS(clientTLS)))
	if err != nil {
		t.Fatal(err)
	}
	defer currentConnection.Close()
	currentReport, err := nodev1.NewAccountingServiceClient(currentConnection).Report(oldControlContext)
	if err != nil {
		t.Fatal(err)
	}
	if err := currentReport.Send(usage); err != nil {
		t.Fatal(err)
	}
	if receipt, err := currentReport.Recv(); err != nil || receipt.AccountedSequence != 1 {
		t.Fatalf("current credential accounting rejected: %v %v", receipt, err)
	}
	budget(t, store.db, budgetID, 1, 99)
	if err := store.db.Model(&model.FleetNode{}).Where("id = ?", p.NodeID).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	_, err = nodev1.NewIdentityServiceClient(connection).Renew(t.Context(), &nodev1.RenewRequest{RequestId: uuid.NewString(), CertificateRequestPem: csr})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("revoked renewal=%v", err)
	}
}
