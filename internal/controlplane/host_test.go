package controlplane

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"google.golang.org/grpc"
	grpccredentials "google.golang.org/grpc/credentials"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/credentials"
)

func TestAuthorityFileCannotBeRecreatedOrRebound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "private", "authority.json")
	if err := CreateAuthorityFile(path, []string{"localhost", "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	first, err := LoadAuthorityFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := CreateAuthorityFile(path, []string{"localhost"}); err == nil {
		t.Fatal("authority silently replaced")
	}
	second, err := LoadAuthorityFile(path)
	if err != nil || second.ControlPlaneID != first.ControlPlaneID {
		t.Fatalf("authority changed: %v", err)
	}
	if _, _, err := first.TLS([]string{"wrong.invalid"}, time.Now()); !errors.Is(err, ErrAuthority) {
		t.Fatalf("wrong hostname accepted: %v", err)
	}
	if _, _, err := first.TLS([]string{"localhost"}, time.Now().AddDate(0, 4, 0)); !errors.Is(err, ErrAuthority) {
		t.Fatalf("expired server certificate accepted: %v", err)
	}
	future := time.Now().AddDate(0, 4, 0)
	renewed, err := RefreshAuthorityFile(path, []string{"localhost", "127.0.0.1"}, future)
	if err != nil {
		t.Fatal(err)
	}
	if renewed.ControlPlaneID != first.ControlPlaneID || !bytes.Equal(renewed.CA, first.CA) || !bytes.Equal(renewed.CAKey, first.CAKey) || bytes.Equal(renewed.ServerKey, first.ServerKey) {
		t.Fatal("server renewal changed authority or reused its leaf key")
	}
	if _, _, err := renewed.TLS([]string{"localhost", "127.0.0.1"}, future); err != nil {
		t.Fatal(err)
	}
	persisted, err := LoadAuthorityFile(path)
	if err != nil || !bytes.Equal(persisted.ServerCertificate, renewed.ServerCertificate) {
		t.Fatalf("renewal was not persisted: %v", err)
	}
}

func TestMasterHostEnrollmentAndDatabaseBinding(t *testing.T) {
	db := testDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "private", "authority.json")
	if err := CreateAuthorityFile(path, []string{"127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	control, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	enroll, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer enroll.Close()
	config := HostConfig{Version: 1, AuthorityFile: path, ControlListen: control.Addr().String(), EnrollmentListen: enroll.Addr().String(), ControlAddress: control.Addr().String(), EnrollmentURL: "https://" + enroll.Addr().String() + "/enroll"}
	host, err := NewHost(t.Context(), store, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewHost(t.Context(), store, config); err != nil {
		t.Fatalf("ordinary master restart rejected: %v", err)
	}
	other := filepath.Join(t.TempDir(), "private", "authority.json")
	if err := CreateAuthorityFile(other, []string{"127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	wrong := config
	wrong.AuthorityFile = other
	if _, err := NewHost(t.Context(), store, wrong); !errors.Is(err, ErrAuthority) {
		t.Fatalf("different master authority adopted existing database: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- host.Serve(ctx, control, enroll) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	packageData, err := host.Enroll(t.Context(), "test-headless-node")
	if err != nil {
		t.Fatal(err)
	}
	vault, err := credentials.Open(filepath.Join(t.TempDir(), "credentials.db"), &packageData.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	defer vault.Close()
	if err := vault.Enroll(t.Context(), packageData.Ticket); err != nil {
		t.Fatal(err)
	}
	tlsConfig, err := vault.TLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	connection, err := grpc.NewClient(control.Addr().String(), grpc.WithTransportCredentials(grpccredentials.NewTLS(tlsConfig)))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	stream, err := nodev1.NewControlServiceClient(connection).Connect(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	identity := packageData.Configuration.Identity
	if err := stream.Send(&nodev1.NodeMessage{Payload: &nodev1.NodeMessage_Hello{Hello: journalHello(t, store, identity)}}); err != nil {
		t.Fatal(err)
	}
	message, err := stream.Recv()
	if err != nil || message.GetAuthority() == nil || message.GetAuthority().ControlPlaneId != host.ControlPlaneID() {
		t.Fatalf("authenticated master stream failed: %v", err)
	}
	if err := stream.CloseSend(); err != nil {
		t.Fatal(err)
	}
	for {
		_, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(packageData.Configuration.TrustBundlePEM) {
		t.Fatal("invalid enrollment trust")
	}
	anonymous, err := grpc.NewClient(control.Addr().String(), grpc.WithTransportCredentials(grpccredentials.NewTLS(&tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS13})))
	if err != nil {
		t.Fatal(err)
	}
	defer anonymous.Close()
	deadline, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	unauth, err := nodev1.NewControlServiceClient(anonymous).Connect(deadline)
	if err == nil {
		err = unauth.Send(&nodev1.NodeMessage{})
		if err == nil {
			_, err = unauth.Recv()
		}
	}
	if err == nil {
		t.Fatal("control listener accepted a missing client certificate")
	}
	client := &http.Client{Timeout: time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12, MaxVersion: tls.VersionTLS12}}}
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, config.EnrollmentURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response, err := client.Do(req); err == nil {
		_ = response.Body.Close()
		t.Fatal("enrollment listener accepted TLS 1.2")
	}
}
