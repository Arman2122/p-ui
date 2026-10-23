package runtime

import (
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Arman2122/p-ui/internal/database/model"
	"github.com/Arman2122/p-ui/internal/util/common"
	"github.com/Arman2122/p-ui/internal/util/netproxy"
	"github.com/Arman2122/p-ui/internal/util/netsafe"
)

// MasterClientCertProvider supplies the master client certificate this panel
// presents to nodes in mtls mode. It is injected by the web layer so the
// runtime package need not import service.
type MasterClientCertProvider func() (tls.Certificate, error)

var (
	masterClientCertMu sync.RWMutex
	masterClientCert   MasterClientCertProvider
	masterCertEpoch    atomic.Uint64
)

// SetMasterClientCertProvider installs the provider used to obtain the master
// client certificate for mtls nodes. Passing nil disables it.
func SetMasterClientCertProvider(p MasterClientCertProvider) {
	masterClientCertMu.Lock()
	defer masterClientCertMu.Unlock()
	masterClientCert = p
	InvalidateMasterClientConnections()
}

// InvalidateMasterClientConnections refreshes cached mTLS pools before their
// next request without interrupting requests already using the old transport.
func InvalidateMasterClientConnections() {
	masterCertEpoch.Add(1)
}

func getMasterClientCert() (tls.Certificate, error) {
	masterClientCertMu.RLock()
	p := masterClientCert
	masterClientCertMu.RUnlock()
	if p == nil {
		return tls.Certificate{}, common.NewError("mtls: master client certificate provider not configured")
	}
	return p()
}

// defaultNodeHTTPClient reaches nodes trusting the system CA store ("verify"
// mode or plain http); shared so connections pool across nodes.
var defaultNodeHTTPClient = &http.Client{
	Transport: &http.Transport{
		MaxIdleConns:        64,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     60 * time.Second,
		DialContext:         netsafe.SSRFGuardedDialContext,
	},
}

func HTTPClientForNode(n *model.Node, proxyURL string) (*http.Client, error) {
	if n.TlsVerifyMode != "mtls" || n.Scheme == "http" {
		return nodeHTTPClient(n, proxyURL)
	}
	node := *n
	transport := &credentialRotatingTransport{build: func() (*http.Transport, error) {
		client, err := nodeHTTPClient(&node, proxyURL)
		if err != nil {
			return nil, err
		}
		underlying, ok := client.Transport.(*http.Transport)
		if !ok {
			return nil, common.NewError("mTLS client transport cannot refresh credentials")
		}
		return underlying, nil
	}}
	if _, err := transport.active(); err != nil {
		return nil, err
	}
	client := &http.Client{Transport: transport}
	if proxyURL != "" {
		client.Timeout = remoteHTTPTimeout
	}
	return client, nil
}

type credentialRotatingTransport struct {
	mu         sync.Mutex
	generation uint64
	current    *http.Transport
	build      func() (*http.Transport, error)
}

func (t *credentialRotatingTransport) active() (*http.Transport, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for t.current == nil || t.generation != masterCertEpoch.Load() {
		generation := masterCertEpoch.Load()
		next, err := t.build()
		if err != nil {
			return nil, err
		}
		if generation != masterCertEpoch.Load() {
			next.CloseIdleConnections()
			continue
		}
		if t.current != nil {
			t.current.CloseIdleConnections()
		}
		t.current, t.generation = next, generation
	}
	return t.current, nil
}

func (t *credentialRotatingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	current, err := t.active()
	if err != nil {
		return nil, err
	}
	return current.RoundTrip(req)
}

func (t *credentialRotatingTransport) CloseIdleConnections() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.current != nil {
		t.current.CloseIdleConnections()
	}
}

func nodeHTTPClient(n *model.Node, proxyURL string) (*http.Client, error) {
	mode := n.TlsVerifyMode
	if mode == "" {
		mode = "verify"
	}
	if proxyURL != "" {
		client, err := netproxy.NewHTTPClient(proxyURL, remoteHTTPTimeout)
		if err != nil {
			return nil, err
		}
		if mode == "verify" || n.Scheme == "http" {
			return client, nil
		}
		transport, ok := client.Transport.(*http.Transport)
		if !ok {
			return client, nil
		}
		tlsCfg, err := tlsConfigForNode(n)
		if err != nil {
			return nil, err
		}
		transport.TLSClientConfig = tlsCfg
		return client, nil
	}
	if mode == "verify" || n.Scheme == "http" {
		return defaultNodeHTTPClient, nil
	}
	tlsCfg, err := tlsConfigForNode(n)
	if err != nil {
		return nil, err
	}
	return &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        64,
			MaxIdleConnsPerHost: 4,
			IdleConnTimeout:     60 * time.Second,
			DialContext:         netsafe.SSRFGuardedDialContext,
			TLSClientConfig:     tlsCfg,
		},
	}, nil
}

func tlsConfigForNode(n *model.Node) (*tls.Config, error) {
	if n.TlsVerifyMode == "mtls" {
		// Present the master client cert; verify the node's server cert against
		// the system roots (no InsecureSkipVerify). mtls authenticates the
		// caller — it does not change how the node's server identity is checked.
		cert, err := getMasterClientCert()
		if err != nil {
			return nil, err
		}
		return &tls.Config{
			Certificates: []tls.Certificate{cert},
			MinVersion:   tls.VersionTLS12,
		}, nil
	}
	tlsCfg := &tls.Config{InsecureSkipVerify: true} // lgtm[go/disabled-certificate-check]
	if n.TlsVerifyMode == "pin" {
		want, err := DecodeCertPin(n.PinnedCertSha256)
		if err != nil {
			return nil, err
		}
		tlsCfg.VerifyConnection = func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return common.NewError("node presented no certificate")
			}
			sum := sha256.Sum256(cs.PeerCertificates[0].Raw)
			if subtle.ConstantTimeCompare(sum[:], want) != 1 {
				return common.NewError("node certificate does not match pinned SHA-256")
			}
			return nil
		}
	}
	return tlsCfg, nil
}

// DecodeCertPin decodes a SHA-256 cert pin given as base64 (Xray's
// pinnedPeerCertSha256 form) or hex with optional colons into 32 raw bytes.
func DecodeCertPin(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, common.NewError("certificate pin is empty")
	}
	if b, err := hex.DecodeString(strings.ReplaceAll(s, ":", "")); err == nil && len(b) == sha256.Size {
		return b, nil
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == sha256.Size {
			return b, nil
		}
	}
	return nil, common.NewError("certificate pin must be a SHA-256 hash (base64 or hex)")
}
