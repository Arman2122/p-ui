package service

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"sync"

	"gorm.io/gorm"

	"github.com/Arman2122/p-ui/internal/database"
	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/util/common"
	"github.com/Arman2122/p-ui/internal/util/crypto"
	"github.com/Arman2122/p-ui/internal/web/runtime"
)

var nodeMtlsCredentialMu sync.Mutex

const (
	settingNodeMtlsCaCert     = "nodeMtlsCaCertPem"
	settingNodeMtlsCaKey      = "nodeMtlsCaKeyPem"
	settingNodeMtlsClientCert = "nodeMtlsClientCertPem"
	settingNodeMtlsClientKey  = "nodeMtlsClientKeyPem"
	settingNodeMtlsClientCA   = "nodeMtlsClientCAPem"
)

// EnsureNodeMtlsCA returns this panel's node-auth CA, minting and persisting it
// on first use and reusing the stored pair thereafter. The CA private key never
// leaves the panel.
func (s *SettingService) EnsureNodeMtlsCA() (crypto.CertKeyPEM, error) {
	nodeMtlsCredentialMu.Lock()
	defer nodeMtlsCredentialMu.Unlock()
	return s.ensureNodeMtlsCA()
}

func (s *SettingService) ensureNodeMtlsCA() (crypto.CertKeyPEM, error) {
	certPem, keyPem, err := loadNodeMtlsPair(settingNodeMtlsCaCert, settingNodeMtlsCaKey)
	if err != nil {
		return crypto.CertKeyPEM{}, err
	}
	if certPem != "" && keyPem != "" {
		if _, err := tls.X509KeyPair([]byte(certPem), []byte(keyPem)); err != nil {
			return crypto.CertKeyPEM{}, fmt.Errorf("stored node mTLS CA pair is invalid: %w", err)
		}
		return crypto.CertKeyPEM{CertPEM: []byte(certPem), KeyPEM: []byte(keyPem)}, nil
	}
	// Fail closed on a half-present pair: regenerating here would silently rotate
	// the CA and break trust on nodes that already hold the old cert. Only mint
	// when neither half exists (first use).
	if certPem != "" || keyPem != "" {
		return crypto.CertKeyPEM{}, common.NewError("node mTLS CA is incomplete: one of cert/key is missing; refusing to regenerate")
	}
	ca, err := crypto.GenerateNodeCA("p-ui node mTLS CA")
	if err != nil {
		return crypto.CertKeyPEM{}, err
	}
	if err := saveNodeMtlsPair(settingNodeMtlsCaCert, settingNodeMtlsCaKey, ca); err != nil {
		return crypto.CertKeyPEM{}, err
	}
	return ca, nil
}

// EnsureMasterClientCert returns the client certificate this panel presents when
// calling its nodes over mTLS, issuing it from the node CA on first use and
// reusing the stored pair thereafter.
func (s *SettingService) EnsureMasterClientCert() (crypto.CertKeyPEM, error) {
	nodeMtlsCredentialMu.Lock()
	defer nodeMtlsCredentialMu.Unlock()
	certPem, keyPem, err := loadNodeMtlsPair(settingNodeMtlsClientCert, settingNodeMtlsClientKey)
	if err != nil {
		return crypto.CertKeyPEM{}, err
	}
	if certPem != "" && keyPem != "" {
		if _, err := tls.X509KeyPair([]byte(certPem), []byte(keyPem)); err != nil {
			return crypto.CertKeyPEM{}, fmt.Errorf("stored master client certificate/key pair is invalid: %w", err)
		}
		return crypto.CertKeyPEM{CertPEM: []byte(certPem), KeyPEM: []byte(keyPem)}, nil
	}
	// Half a stored pair signals corrupted settings; reissuing would rotate the
	// master client credential (and indirectly the CA). Only mint on first use.
	if certPem != "" || keyPem != "" {
		return crypto.CertKeyPEM{}, common.NewError("master client cert is incomplete: one of cert/key is missing; refusing to reissue")
	}
	ca, err := s.ensureNodeMtlsCA()
	if err != nil {
		return crypto.CertKeyPEM{}, err
	}
	client, err := crypto.IssueClientCert(ca, "p-ui master")
	if err != nil {
		return crypto.CertKeyPEM{}, err
	}
	if err := saveNodeMtlsPair(settingNodeMtlsClientCert, settingNodeMtlsClientKey, client); err != nil {
		return crypto.CertKeyPEM{}, err
	}
	return client, nil
}

func loadNodeMtlsPair(certKey, keyKey string) (string, string, error) {
	var rows []model.Setting
	if err := database.GetDB().Where("key IN ?", []string{certKey, keyKey}).Order("id ASC").Find(&rows).Error; err != nil {
		return "", "", err
	}
	values := map[string]string{}
	for _, row := range rows {
		if _, exists := values[row.Key]; !exists {
			values[row.Key] = row.Value
		}
	}
	return values[certKey], values[keyKey], nil
}

func saveNodeMtlsPair(certKey, keyKey string, pair crypto.CertKeyPEM) error {
	err := database.GetDB().Transaction(func(tx *gorm.DB) error {
		for _, setting := range []model.Setting{
			{Key: certKey, Value: string(pair.CertPEM)},
			{Key: keyKey, Value: string(pair.KeyPEM)},
		} {
			result := tx.Model(&model.Setting{}).Where("key = ?", setting.Key).Update("value", setting.Value)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected == 0 {
				if err := tx.Create(&setting).Error; err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err == nil {
		runtime.InvalidateMasterClientConnections()
	}
	return err
}

// NodeMtlsClientCAPool builds the trust pool used as the panel listener's
// ClientCAs for incoming node-API client certificates. It returns (nil, nil)
// when no trust CA is configured, so mTLS stays off and the listener behaves
// exactly as before.
func (s *SettingService) NodeMtlsClientCAPool() (*x509.CertPool, error) {
	caPem, err := s.getString(settingNodeMtlsClientCA)
	if err != nil {
		return nil, err
	}
	if caPem == "" {
		return nil, nil
	}
	certs, err := parseCertificateBundlePEM([]byte(caPem))
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNodeMtlsTrustBundleInvalid, err)
	}
	pool := x509.NewCertPool()
	for _, cert := range certs {
		pool.AddCert(cert)
	}
	return pool, nil
}

// ErrNodeMtlsTrustBundleInvalid distinguishes unusable certificates from failed settings reads.
var ErrNodeMtlsTrustBundleInvalid = errors.New("nodeMtlsClientCAPem is not a valid certificate bundle")

func parseCertificateBundlePEM(bundle []byte) ([]*x509.Certificate, error) {
	rest := bytes.TrimSpace(bundle)
	if len(rest) == 0 {
		return nil, errors.New("certificate bundle is empty")
	}
	var certs []*x509.Certificate
	const begin = "-----BEGIN CERTIFICATE-----"
	const end = "-----END CERTIFICATE-----"
	for len(rest) > 0 {
		last := bytes.Index(rest, []byte(end))
		if !bytes.HasPrefix(rest, []byte(begin)) || last < 0 || bytes.Contains(rest[len(begin):last], []byte("-----BEGIN")) {
			return nil, errors.New("certificate bundle contains malformed or non-certificate data")
		}
		block, remaining := pem.Decode(rest[:last+len(end)])
		if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(remaining)) != 0 {
			return nil, errors.New("certificate bundle contains malformed PEM data")
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("certificate bundle contains an invalid certificate: %w", err)
		}
		certs = append(certs, cert)
		rest = bytes.TrimSpace(rest[last+len(end):])
	}
	return certs, nil
}
