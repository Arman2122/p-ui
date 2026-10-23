package service

import (
	"bytes"
	"errors"
	"strings"
	"sync"
	"testing"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/util/crypto"
)

func TestNodeMtlsPairPersistenceIsAtomic(t *testing.T) {
	for _, master := range []bool{false, true} {
		name := "CA"
		if master {
			name = "master"
		}
		t.Run(name, func(t *testing.T) {
			s := setupSettingMtlsDB(t)
			certKey, keyKey := settingNodeMtlsCaCert, settingNodeMtlsCaKey
			ensure := s.EnsureNodeMtlsCA
			if master {
				if _, err := s.EnsureNodeMtlsCA(); err != nil {
					t.Fatal(err)
				}
				certKey, keyKey, ensure = settingNodeMtlsClientCert, settingNodeMtlsClientKey, s.EnsureMasterClientCert
			}
			rejected := errors.New("private-key write rejected")
			db := database.GetDB()
			callback := "test:reject_mtls_private_key"
			if err := db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
				if setting, ok := tx.Statement.Dest.(*model.Setting); ok && setting.Key == keyKey {
					_ = tx.AddError(rejected)
				}
			}); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = db.Callback().Create().Remove(callback) })
			if _, err := ensure(); !errors.Is(err, rejected) {
				t.Fatalf("ensure error = %v, want injected private-key failure", err)
			}
			var rows int64
			if err := db.Model(&model.Setting{}).Where("key IN ?", []string{certKey, keyKey}).Count(&rows).Error; err != nil {
				t.Fatal(err)
			}
			if rows != 0 {
				t.Fatalf("failed pair write left %d credential rows, want none", rows)
			}
		})
	}
}

func TestNodeMtlsConcurrentFirstUseKeepsOnePair(t *testing.T) {
	s := setupSettingMtlsDB(t)
	const callers = 8
	var wg sync.WaitGroup
	results := make([]crypto.CertKeyPEM, callers)
	errs := make([]error, callers)
	for i := range callers {
		wg.Go(func() { results[i], errs[i] = s.EnsureMasterClientCert() })
	}
	wg.Wait()
	for i := range callers {
		if errs[i] != nil {
			t.Fatalf("caller %d: %v", i, errs[i])
		}
		if !bytes.Equal(results[i].CertPEM, results[0].CertPEM) || !bytes.Equal(results[i].KeyPEM, results[0].KeyPEM) {
			t.Fatalf("caller %d received a different master credential", i)
		}
	}
}

func TestNodeMtlsRejectsMismatchedStoredPair(t *testing.T) {
	s := setupSettingMtlsDB(t)
	a, err := crypto.GenerateNodeCA("first")
	if err != nil {
		t.Fatal(err)
	}
	b, err := crypto.GenerateNodeCA("second")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.setString(settingNodeMtlsClientCert, string(a.CertPEM)); err != nil {
		t.Fatal(err)
	}
	if err := s.setString(settingNodeMtlsClientKey, string(b.KeyPEM)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.EnsureMasterClientCert(); err == nil || !strings.Contains(err.Error(), "private key does not match public key") {
		t.Fatalf("mismatched stored pair error = %v", err)
	}
}

func TestNodeMtlsRejectsEveryInvalidBundleEntry(t *testing.T) {
	s := setupSettingMtlsDB(t)
	ca, err := crypto.GenerateNodeCA("bundle")
	if err != nil {
		t.Fatal(err)
	}
	for _, tail := range []string{
		"garbage", "-----BEGIN CERTIFICATE-----\nnot base64\n-----END CERTIFICATE-----\n",
		"-----BEGIN CERTIFICATE-----\nnot base64\n" + string(ca.CertPEM),
		string(ca.KeyPEM),
	} {
		bundle := string(ca.CertPEM) + tail
		if err := (&NodeService{}).SetNodeMtlsTrustCA(bundle); err == nil || !strings.Contains(err.Error(), "certificate bundle") {
			t.Fatalf("accepted invalid tail %q: %v", tail, err)
		}
		if err := s.setString(settingNodeMtlsClientCA, bundle); err != nil {
			t.Fatal(err)
		}
		if _, err := s.NodeMtlsClientCAPool(); err == nil || !strings.Contains(err.Error(), "certificate bundle") {
			t.Fatalf("accepted stored invalid tail %q: %v", tail, err)
		}
	}
}
