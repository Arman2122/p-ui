package controlplane

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/compatibility"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

func TestEnrollmentTicketAndCSRBindingPostgres(t *testing.T) {
	db := testDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := store.CreateEnrollment(context.Background(), "node-one")
	if err != nil {
		t.Fatal(err)
	}
	plane := uuid.NewString()
	p := identity.Principal{ControlPlaneID: plane, NodeID: enrollment.NodeID, InstallationID: enrollment.InstallationID}
	serverTLS, clientTLS, issuer, csr := tlsPair(t, p)
	handler, err := NewEnrollmentHandler(store, plane, issuer)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.TLS = serverTLS
	server.StartTLS()
	defer server.Close()
	clientTLS.Certificates = nil
	transport := &http.Transport{TLSClientConfig: clientTLS}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	request := &nodev1.EnrollRequest{
		RequestId: uuid.NewString(), Ticket: enrollment.Ticket,
		InstallationId: enrollment.InstallationID, CertificateRequestPem: csr,
	}
	post := func(request *nodev1.EnrollRequest) (int, []byte) {
		t.Helper()
		body, err := protojson.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		response, err := client.Post(server.URL+"/enroll", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("enrollment response may be cached")
		}
		return response.StatusCode, data
	}
	code, body := post(request)
	if code != http.StatusOK {
		t.Fatalf("enroll status=%d body=%s", code, body)
	}
	var first nodev1.EnrollResponse
	if err := protojson.Unmarshal(body, &first); err != nil {
		t.Fatal(err)
	}
	if first.NodeId != p.NodeID || first.ControlPlaneId != plane || first.ProtocolVersion != compatibility.Protocol {
		t.Fatalf("identity=%v", &first)
	}
	code, body = post(request)
	var replay nodev1.EnrollResponse
	if err := protojson.Unmarshal(body, &replay); err != nil {
		t.Fatal(err)
	}
	if code != http.StatusOK || !bytes.Equal(first.CertificateChainPem, replay.CertificateChainPem) {
		t.Fatalf("replay changed certificate, status=%d", code)
	}
	other := proto.Clone(request).(*nodev1.EnrollRequest)
	other.RequestId = uuid.NewString()
	if code, _ := post(other); code != http.StatusForbidden {
		t.Fatalf("ticket reuse status=%d", code)
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	if err != nil {
		t.Fatal(err)
	}
	other = proto.Clone(request).(*nodev1.EnrollRequest)
	other.CertificateRequestPem = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der})
	if code, _ := post(other); code != http.StatusForbidden {
		t.Fatalf("changed key status=%d", code)
	}
	if err := db.Model(&model.FleetNode{}).Where("id = ?", p.NodeID).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if code, _ := post(request); code != http.StatusForbidden {
		t.Fatalf("revoked node replay status=%d", code)
	}

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "http://control.test/enroll", bytes.NewReader(nil)))
	if recorder.Code != http.StatusUpgradeRequired {
		t.Fatalf("plaintext status=%d", recorder.Code)
	}
}
