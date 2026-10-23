package controlplane

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/encoding/protojson"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/database/model"
	vault "github.com/Arman2122/p-ui/internal/node/credentials"
	"github.com/Arman2122/p-ui/internal/node/identity"
)

func TestKeyRecoverySurvivesLostResponseAndExpiredCertificate(t *testing.T) {
	db := testDB(t)
	store, err := NewStore(db)
	if err != nil {
		t.Fatal(err)
	}
	material, err := NewAuthority([]string{"127.0.0.1"}, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	serverTLS, issuer, err := material.TLS([]string{"127.0.0.1"}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	enrollment, err := store.CreateEnrollment(t.Context(), "lost-response-node")
	if err != nil {
		t.Fatal(err)
	}
	p := identity.Principal{ControlPlaneID: material.ControlPlaneID, NodeID: enrollment.NodeID, InstallationID: enrollment.InstallationID}
	handler, err := NewEnrollmentHandler(store, p.ControlPlaneID, issuer)
	if err != nil {
		t.Fatal(err)
	}
	var enrollDrop, recoverDrop atomic.Bool
	enrollDrop.Store(true)
	recoverDrop.Store(true)
	var initialCSR atomic.Pointer[[]byte]
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		drop := r.URL.Path == "/enroll" && enrollDrop.Swap(false) || r.URL.Path == "/recover" && recoverDrop.Swap(false)
		if r.URL.Path == "/enroll" {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			request := new(nodev1.EnrollRequest)
			if err := protojson.Unmarshal(body, request); err != nil {
				t.Error(err)
				return
			}
			csr := bytes.Clone(request.CertificateRequestPem)
			initialCSR.Store(&csr)
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		if !drop {
			handler.ServeHTTP(w, r)
			return
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, r)
		if recorder.Code != http.StatusOK {
			t.Errorf("before lost response: HTTP %d", recorder.Code)
		}
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = connection.Close()
	}))
	server.TLS = serverTLS
	server.StartTLS()
	defer server.Close()
	path := filepath.Join(t.TempDir(), "credentials.db")
	config := vault.Config{Identity: p, ControlAddress: "127.0.0.1:7443", EnrollmentURL: server.URL + "/enroll", TrustBundlePEM: material.CA}
	local, err := vault.Open(path, &config)
	if err != nil {
		t.Fatal(err)
	}
	if err := local.Enroll(t.Context(), enrollment.Ticket); err == nil {
		t.Fatal("lost enrollment response accepted")
	}
	if err := db.Model(&model.FleetEnrollment{}).Where("node_id = ?", p.NodeID).Update("expires_unix_milli", time.Now().Add(-time.Hour).UnixMilli()).Error; err != nil {
		t.Fatal(err)
	}
	if err := local.Recover(t.Context()); err == nil {
		t.Fatal("lost recovery response accepted")
	}
	if err := local.Close(); err != nil {
		t.Fatal(err)
	}
	local, err = vault.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := local.Recover(t.Context()); err != nil {
		t.Fatal(err)
	}
	tlsConfig, err := local.TLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	before, err := tlsConfig.GetClientCertificate(&tls.CertificateRequestInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if err := local.Close(); err != nil {
		t.Fatal(err)
	}
	expired, err := identity.Issue(issuer.Certificate, issuer.Signer, *initialCSR.Load(), p, time.Now().Add(-13*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&model.FleetCredential{}).Where("node_id = ?", p.NodeID).Updates(map[string]any{"certificate": expired, "issued_unix_milli": time.Now().Add(-13 * time.Hour).UnixMilli()}).Error; err != nil {
		t.Fatal(err)
	}
	journal, err := bolt.Open(path, 0o600, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = journal.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket([]byte("credentials"))
		var active map[string]json.RawMessage
		if err := json.Unmarshal(bucket.Get([]byte("active")), &active); err != nil {
			return err
		}
		chain, err := json.Marshal(expired)
		if err != nil {
			return err
		}
		active["Chain"] = chain
		encoded, err := json.Marshal(active)
		if err != nil {
			return err
		}
		return bucket.Put([]byte("active"), encoded)
	})
	closeErr := journal.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	local, err = vault.Open(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	if err := local.Renew(t.Context(), nil); err != nil {
		t.Fatalf("automatic expired-certificate recovery: %v", err)
	}
	tlsConfig, err = local.TLSConfig()
	if err != nil {
		t.Fatal(err)
	}
	after, err := tlsConfig.GetClientCertificate(&tls.CertificateRequestInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(before.Leaf.RawSubjectPublicKeyInfo, after.Leaf.RawSubjectPublicKeyInfo) || !after.Leaf.NotAfter.After(time.Now()) {
		t.Fatal("recovery did not activate a fresh valid credential")
	}
}
