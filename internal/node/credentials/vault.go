package credentials

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	bolt "go.etcd.io/bbolt"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
	"github.com/Arman2122/p-ui/internal/node/compatibility"
	"github.com/Arman2122/p-ui/internal/node/identity"
	"github.com/Arman2122/p-ui/internal/node/privatefile"
)

var (
	ErrConfiguration = errors.New("invalid node enrollment configuration")
	ErrPending       = errors.New("node credential request mismatch")
	ErrCertificate   = errors.New("node certificate rejected")
	ErrNotEnrolled   = errors.New("node is not enrolled")
	vaultBucket      = []byte("credentials")
)

type Config struct {
	Identity       identity.Principal `json:"identity"`
	ControlAddress string             `json:"controlAddress"`
	ServerName     string             `json:"serverName,omitempty"`
	EnrollmentURL  string             `json:"enrollmentURL"`
	TrustBundlePEM []byte             `json:"trustBundlePEM"`
}

type EnrollmentPackage struct {
	Configuration    Config `json:"configuration"`
	Ticket           string `json:"ticket"`
	ExpiresUnixMilli int64  `json:"expiresUnixMilli"`
}

type request struct {
	ID  string
	Key []byte
	CSR []byte
}

type material struct {
	Key   []byte
	Chain []byte
}

type Vault struct {
	operationMu sync.Mutex
	db          *bolt.DB
	config      Config
	roots       *x509.CertPool
	certificate atomic.Pointer[tls.Certificate]
}

func (c Config) validate() (*x509.CertPool, error) {
	if _, err := c.Identity.URI(); err != nil || c.Identity.Serial != "" {
		return nil, ErrConfiguration
	}
	host, port, err := net.SplitHostPort(c.ControlAddress)
	if err != nil || host == "" {
		return nil, ErrConfiguration
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return nil, ErrConfiguration
	}
	u, err := url.Parse(c.EnrollmentURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/enroll" {
		return nil, ErrConfiguration
	}
	roots := x509.NewCertPool()
	if len(c.TrustBundlePEM) > 64<<10 || !roots.AppendCertsFromPEM(c.TrustBundlePEM) {
		return nil, ErrConfiguration
	}
	return roots, nil
}

func Open(path string, initial *Config) (*Vault, error) {
	if initial != nil {
		if _, err := initial.validate(); err != nil {
			return nil, err
		}
	}
	if info, err := os.Lstat(path); err == nil {
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return nil, ErrConfiguration
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	} else if initial == nil {
		return nil, ErrNotEnrolled
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: time.Second, OpenFile: privatefile.Open})
	if err != nil {
		return nil, err
	}
	v := &Vault{db: db}
	err = db.Update(func(tx *bolt.Tx) error {
		bucket, err := tx.CreateBucketIfNotExists(vaultBucket)
		if err != nil {
			return err
		}
		saved := bucket.Get([]byte("config"))
		if saved == nil {
			if initial == nil {
				return ErrNotEnrolled
			}
			saved, err = json.Marshal(initial)
			if err != nil {
				return err
			}
			if err := bucket.Put([]byte("config"), saved); err != nil {
				return err
			}
		} else if initial != nil {
			encoded, err := json.Marshal(initial)
			if err != nil || !bytes.Equal(encoded, saved) {
				return ErrConfiguration
			}
		}
		if err := json.Unmarshal(saved, &v.config); err != nil {
			return ErrConfiguration
		}
		v.roots, err = v.config.validate()
		if err != nil {
			return err
		}
		if encoded := bucket.Get([]byte("active")); encoded != nil {
			var active material
			if err := json.Unmarshal(encoded, &active); err != nil {
				return ErrCertificate
			}
			certificate, err := v.verify(active, false)
			if err != nil {
				return err
			}
			v.certificate.Store(certificate)
		}
		return nil
	})
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	return v, nil
}

func (v *Vault) Close() error { return v.db.Close() }

func (v *Vault) Config() Config {
	result := v.config
	result.TrustBundlePEM = bytes.Clone(result.TrustBundlePEM)
	return result
}

func (v *Vault) pending() (*request, error) {
	var pending request
	err := v.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(vaultBucket)
		if data := bucket.Get([]byte("pending")); data != nil {
			return json.Unmarshal(data, &pending)
		}
		key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err != nil {
			return err
		}
		der, err := x509.MarshalPKCS8PrivateKey(key)
		if err != nil {
			return err
		}
		csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
		if err != nil {
			return err
		}
		pending = request{
			ID: uuid.NewString(), Key: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}),
			CSR: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr}),
		}
		data, err := json.Marshal(pending)
		if err != nil {
			return err
		}
		return bucket.Put([]byte("pending"), data)
	})
	return &pending, err
}

func (v *Vault) accept(requestID string, response *nodev1.EnrollResponse) error {
	if response == nil || response.ProtocolVersion != compatibility.Protocol || response.NodeId != v.config.Identity.NodeID || response.ControlPlaneId != v.config.Identity.ControlPlaneID {
		return ErrCertificate
	}
	var certificate *tls.Certificate
	err := v.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(vaultBucket)
		var pending request
		if err := json.Unmarshal(bucket.Get([]byte("pending")), &pending); err != nil || pending.ID != requestID {
			return ErrPending
		}
		active := material{Key: pending.Key, Chain: response.CertificateChainPem}
		var err error
		certificate, err = v.verify(active, true)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(active)
		if err != nil {
			return err
		}
		if err := bucket.Put([]byte("active"), encoded); err != nil {
			return err
		}
		if err := bucket.Delete([]byte("recovery")); err != nil {
			return err
		}
		return bucket.Delete([]byte("pending"))
	})
	if err == nil {
		v.certificate.Store(certificate)
	}
	return err
}

func (v *Vault) verify(active material, current bool) (*tls.Certificate, error) {
	if len(active.Chain) > 64<<10 || len(active.Key) > 8<<10 {
		return nil, ErrCertificate
	}
	pair, err := tls.X509KeyPair(active.Chain, active.Key)
	if err != nil || pair.Leaf == nil {
		return nil, ErrCertificate
	}
	leaf := pair.Leaf
	u, _ := v.config.Identity.URI()
	if leaf.IsCA || len(leaf.URIs) != 1 || leaf.URIs[0].String() != u.String() || leaf.NotAfter.Sub(leaf.NotBefore) > identity.Lifetime+5*time.Minute {
		return nil, ErrCertificate
	}
	intermediates := x509.NewCertPool()
	for _, der := range pair.Certificate[1:] {
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, ErrCertificate
		}
		intermediates.AddCert(cert)
	}
	when := time.Now()
	if !current {
		when = leaf.NotBefore.Add(time.Second)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: v.roots, Intermediates: intermediates, CurrentTime: when, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return nil, fmt.Errorf("%w: chain validation failed", ErrCertificate)
	}
	return &pair, nil
}

func (v *Vault) TLSConfig() (*tls.Config, error) {
	if v.certificate.Load() == nil {
		return nil, ErrNotEnrolled
	}
	name := v.config.ServerName
	if name == "" {
		name, _, _ = net.SplitHostPort(v.config.ControlAddress)
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS13, RootCAs: v.roots.Clone(), ServerName: name,
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) {
			pair := v.certificate.Load()
			if pair == nil || time.Now().Before(pair.Leaf.NotBefore) || !time.Now().Before(pair.Leaf.NotAfter) {
				return nil, ErrCertificate
			}
			return pair, nil
		},
	}, nil
}

func (v *Vault) RenewalDue() bool {
	certificate := v.certificate.Load()
	return certificate == nil || time.Until(certificate.Leaf.NotAfter) < identity.Lifetime/2
}
