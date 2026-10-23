package controlplane

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

func recoveryCSR(t *testing.T) (crypto.Signer, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	if err != nil {
		t.Fatal(err)
	}
	return key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr})
}

func TestRecoveryRequiresCurrentRegisteredKey(t *testing.T) {
	db := testDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	material, err := NewAuthority([]string{"localhost"}, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	_, issuer, err := material.TLS([]string{"localhost"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := store.CreateEnrollment(t.Context(), "recoverable")
	if err != nil {
		t.Fatal(err)
	}
	oldKey, oldCSR := recoveryCSR(t)
	_, err = store.Redeem(t.Context(), material.ControlPlaneID, issuer, &nodev1.EnrollRequest{Ticket: enrollment.Ticket, InstallationId: enrollment.InstallationID, RequestId: uuid.NewString(), CertificateRequestPem: oldCSR})
	if err != nil {
		t.Fatal(err)
	}
	p := identity.Principal{ControlPlaneID: material.ControlPlaneID, NodeID: enrollment.NodeID, InstallationID: enrollment.InstallationID}
	expired, err := identity.Issue(issuer.Certificate, issuer.Signer, oldCSR, p, time.Now().Add(-13*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.FleetEnrollment{}).Where("node_id = ?", p.NodeID).Update("certificate", expired).Error; err != nil {
		t.Fatal(err)
	}
	challenge, err := store.RecoveryChallenge(t.Context(), &nodev1.RecoveryChallengeRequest{NodeId: p.NodeID, InstallationId: p.InstallationID})
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := store.RecoveryChallenge(t.Context(), &nodev1.RecoveryChallengeRequest{NodeId: p.NodeID, InstallationId: p.InstallationID})
	if err != nil || repeated.ChallengeId != challenge.ChallengeId {
		t.Fatalf("unauthenticated challenge replaced live nonce: %v", err)
	}
	newKey, csr := recoveryCSR(t)
	request := &nodev1.RecoveryRequest{NodeId: p.NodeID, InstallationId: p.InstallationID, RequestId: uuid.NewString(), ChallengeId: challenge.ChallengeId, CertificateRequestPem: csr}
	digest, err := identity.RecoveryDigest(p.ControlPlaneID, challenge, request)
	if err != nil {
		t.Fatal(err)
	}
	request.Proof, err = identity.SignRecovery(newKey, digest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecoverKey(t.Context(), p.ControlPlaneID, issuer, request); !errors.Is(err, ErrEnrollment) {
		t.Fatalf("unknown key recovered node: %v", err)
	}
	request.Proof, err = identity.SignRecovery(oldKey, digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.FleetRecoveryChallenge{}).Where("node_id = ?", p.NodeID).Update("expires_unix_milli", time.Now().Add(-time.Second).UnixMilli()).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecoverKey(t.Context(), p.ControlPlaneID, issuer, request); !errors.Is(err, ErrEnrollment) {
		t.Fatalf("expired challenge recovered node: %v", err)
	}
	if err := db.Model(&model.FleetRecoveryChallenge{}).Where("node_id = ?", p.NodeID).Update("expires_unix_milli", time.Now().Add(time.Minute).UnixMilli()).Error; err != nil {
		t.Fatal(err)
	}
	response, err := store.RecoverKey(t.Context(), p.ControlPlaneID, issuer, request)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := store.RecoverKey(t.Context(), p.ControlPlaneID, issuer, request)
	if err != nil || !bytes.Equal(response.CertificateChainPem, replay.CertificateChainPem) {
		t.Fatalf("recovery replay changed credential: %v", err)
	}
	challenge, err = store.RecoveryChallenge(t.Context(), &nodev1.RecoveryChallengeRequest{NodeId: p.NodeID, InstallationId: p.InstallationID})
	if err != nil {
		t.Fatal(err)
	}
	request.ChallengeId, request.RequestId = challenge.ChallengeId, uuid.NewString()
	digest, err = identity.RecoveryDigest(p.ControlPlaneID, challenge, request)
	if err != nil {
		t.Fatal(err)
	}
	request.Proof, err = identity.SignRecovery(oldKey, digest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecoverKey(t.Context(), p.ControlPlaneID, issuer, request); !errors.Is(err, ErrEnrollment) {
		t.Fatalf("superseded private key recovered node: %v", err)
	}
	request.Proof, err = identity.SignRecovery(newKey, digest)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.FleetNode{}).Where("id = ?", p.NodeID).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecoverKey(t.Context(), p.ControlPlaneID, issuer, request); !errors.Is(err, ErrScope) {
		t.Fatalf("revoked node recovered credential: %v", err)
	}
}
